from contextlib import redirect_stdout
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('release', Path(__file__).with_name('replica-release.py'))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


def original(version='a', secret='private-test-secret', worker=False):
    env = {'RUN_INLINE_WORKERS': 'false', 'RUN_MIGRATIONS': 'true', 'JWT_SECRET': secret,
           'REDIS_URL': 'redis://private', 'DB_MAX_CONNS': '10' if worker else '20'}
    return {'Id': 'original-' + version, 'Image': 'sha256:' + version * 64,
            'Config': {'Image': 'local:' + version * 40, 'Env': [k + '=' + v for k, v in env.items()]},
            'State': {'Health': {'Status': 'healthy'}}}


def replicas(api):
    env = release.normalized(release.pilot.environment(api), desired=True)
    return [{'Id': 'copy-' + str(i), 'Image': api['Image'],
             'Config': {'Env': [k + '=' + v for k, v in dict(env, HOSTNAME='copy-' + str(i)).items()]},
             'State': {'Health': {'Status': 'healthy'}}} for i in range(2)]


class FakeHost:
    def __init__(self, api, worker, copies, failure=None, deploy_during_refresh=False):
        self.api, self.worker, self.copies = api, worker, copies
        self.active, self.failure, self.deploy_during_refresh = True, failure, deploy_during_refresh
        self.calls = []

    def snapshot(self):
        self.calls.append('snapshot')
        return self.api, self.worker, self.copies

    def original_ready(self, _api):
        self.calls.append('original-ready')

    def fallback(self):
        self.calls.append('fallback')
        self.active = False

    def refresh(self, api):
        self.calls.append('refresh')
        if self.failure:
            raise RuntimeError(self.failure)
        self.copies = replicas(api)
        if self.deploy_during_refresh:
            self.api, self.worker = original('c'), original('c', worker=True)

    def activate(self):
        self.calls.append('activate')
        self.active = True


class Releases(unittest.TestCase):
    def run_case(self, host, maximum=100, enabled=True):
        with tempfile.TemporaryDirectory() as directory:
            marker = Path(directory) / 'enabled'
            if enabled:
                marker.write_text('enabled')
            with patch.object(release, 'ENABLED', marker), patch.object(release, 'owned_route', side_effect=lambda: host.active):
                try:
                    result = release.reconcile(host, maximum)
                    return result, marker.exists()
                except Exception as error:
                    return error, marker.exists()

    def test_install_policy_adopts_existing_aligned_replicas(self):
        api, worker = original(), original(worker=True)
        plan = release.decide(api, worker, replicas(api), 100)
        self.assertEqual(plan['action'], 'aligned')
        self.assertEqual(plan['connection_budget'], 84)
        self.assertNotIn('private', json.dumps(plan))

    def test_stable_timer_tick_does_not_restart_containers(self):
        api = original()
        host = FakeHost(api, original(worker=True), replicas(api))
        result, enabled = self.run_case(host)
        self.assertEqual(result['action'], 'aligned')
        self.assertTrue(enabled)
        self.assertEqual(host.calls, ['snapshot', 'original-ready'])

    def test_release_falls_back_refreshes_and_then_reactivates(self):
        host = FakeHost(original('b'), original('b', worker=True), replicas(original()))
        result, enabled = self.run_case(host)
        self.assertEqual(result['action'], 'updated')
        self.assertTrue(enabled)
        self.assertEqual(host.calls, ['snapshot', 'original-ready', 'fallback', 'refresh', 'snapshot', 'activate'])
        self.assertTrue(host.active)

    def test_secret_rotation_with_same_image_requires_refresh(self):
        api = original(secret='new-private-secret')
        plan = release.decide(api, original(worker=True), replicas(original()), 100)
        self.assertEqual(plan['action'], 'refresh')
        self.assertNotIn('new-private-secret', json.dumps(plan))

    def test_inherited_read_pool_cannot_exceed_connection_budget(self):
        api = original()
        api['Config']['Env'].append('DATABASE_READ_URL=private')
        host = FakeHost(api, original(worker=True), replicas(original()))
        result, enabled = self.run_case(host)
        self.assertIsInstance(result, ValueError)
        self.assertFalse(enabled)
        self.assertFalse(host.active)
        self.assertNotIn('refresh', host.calls)

    def test_waits_for_matching_worker_without_updating_replicas(self):
        host = FakeHost(original('b'), original(worker=True), replicas(original()))
        result, enabled = self.run_case(host)
        self.assertEqual(result['action'], 'wait-worker')
        self.assertTrue(enabled)
        self.assertFalse(host.active)
        self.assertNotIn('refresh', host.calls)

    def test_waits_when_worker_points_to_another_database(self):
        worker = original('b', worker=True)
        worker['Config']['Env'].append('DATABASE_URL=private-different-database')
        host = FakeHost(original('b'), worker, replicas(original()))
        result, enabled = self.run_case(host)
        self.assertEqual(result['action'], 'wait-worker')
        self.assertTrue(enabled)
        self.assertFalse(host.active)
        self.assertNotIn('private-different-database', json.dumps(result))
        self.assertNotIn('refresh', host.calls)

    def test_unhealthy_original_does_not_replace_working_copies(self):
        api = original('b')
        api['State']['Health']['Status'] = 'unhealthy'
        host = FakeHost(api, original('b', worker=True), replicas(original()))
        result, enabled = self.run_case(host)
        self.assertEqual(result['action'], 'wait-api')
        self.assertTrue(enabled)
        self.assertTrue(host.active)
        self.assertEqual(host.calls, ['snapshot'])

    def test_failed_refresh_leaves_original_release_and_pauses(self):
        host = FakeHost(original('b'), original('b', worker=True), replicas(original()), failure='fixture refresh failed')
        result, enabled = self.run_case(host)
        self.assertIsInstance(result, RuntimeError)
        self.assertFalse(enabled)
        self.assertFalse(host.active)
        self.assertEqual(host.api['Id'], 'original-b')
        self.assertNotIn('activate', host.calls)

    def test_deployment_during_refresh_does_not_activate_stale_copies(self):
        host = FakeHost(original('b'), original('b', worker=True), replicas(original()), deploy_during_refresh=True)
        result, enabled = self.run_case(host)
        self.assertIsInstance(result, RuntimeError)
        self.assertFalse(enabled)
        self.assertFalse(host.active)
        self.assertNotIn('activate', host.calls)

    def test_disabled_marker_prevents_mutation_after_manual_rollback(self):
        api = original()
        host = FakeHost(api, original(worker=True), replicas(api))
        result, enabled = self.run_case(host, enabled=False)
        self.assertEqual(result['action'], 'disabled')
        self.assertFalse(enabled)
        self.assertEqual(host.calls, [])

    def test_unknown_extra_replicas_are_not_automatically_resized(self):
        api = original()
        host = FakeHost(api, original(worker=True), replicas(api) + replicas(api))
        result, enabled = self.run_case(host)
        self.assertIsInstance(result, ValueError)
        self.assertFalse(enabled)
        self.assertNotIn('refresh', host.calls)

    def test_manual_route_rollback_disables_timer_marker(self):
        with tempfile.TemporaryDirectory() as directory:
            runtime = Path(directory)
            target, state = runtime / 'route.yaml', runtime / 'state.json'
            content = b'{"http":{}}'
            target.write_bytes(content)
            state.write_text(json.dumps({'path': str(target), 'sha256': release.route.digest(content)}))
            enabled = runtime / 'auto-release-enabled'
            enabled.write_text('enabled')
            with patch.object(release.route, 'RUNTIME', runtime), patch.object(release.route, 'ROUTE', target), patch.object(release.route, 'STATE', state), redirect_stdout(io.StringIO()):
                release.route.rollback()
            self.assertFalse(enabled.exists())

    @unittest.skipIf(os.name == 'nt', 'Linux flock validation runs in CI')
    def test_lock_prevents_overlapping_route_and_release_operations(self):
        with tempfile.TemporaryDirectory() as directory:
            runtime = Path(directory)
            with release.pilot.operation_lock(runtime):
                with self.assertRaises(RuntimeError):
                    with release.pilot.operation_lock(runtime):
                        self.fail('overlapping operation acquired lock')

    def test_atomic_private_writes_never_overwrite_a_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'state'
            release.atomic_private(path, 'first')
            release.atomic_private(path, 'second')
            self.assertEqual(path.read_text(), 'second')
            if os.name != 'nt':
                link = Path(directory) / 'link'
                link.symlink_to(path)
                with self.assertRaises(ValueError):
                    release.atomic_private(link, 'not allowed')
                self.assertEqual(path.read_text(), 'second')

    def test_preview_has_no_writes_or_secret_output(self):
        api = original(secret='private-do-not-print')
        host = FakeHost(api, original(worker=True), replicas(api))
        output = io.StringIO()
        with patch.object(release.sys, 'argv', ['release']), patch.object(release, 'Host', return_value=host), patch.object(release, 'owned_route', return_value=True), patch.object(release, 'atomic_private', side_effect=AssertionError('must not write')), redirect_stdout(output):
            release.main()
        self.assertIn('Preview only', output.getvalue())
        self.assertNotIn('private-do-not-print', output.getvalue())
        self.assertEqual(host.calls, ['snapshot'])

    def test_install_freezes_code_and_does_not_restart_live_containers(self):
        with tempfile.TemporaryDirectory() as directory:
            root, runtime, units = [Path(directory) / name for name in ('source', 'runtime', 'units')]
            for folder in (root, runtime, units):
                folder.mkdir(mode=0o700)
            names = ['scripts/replica-pilot.py', 'scripts/replica-route.py', 'scripts/replica-release.py',
                     'deploy/scaling/compose.yml', 'deploy/scaling/haproxy.cfg']
            for name in names:
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text('synthetic reviewed content')
            api = original()
            host = FakeHost(api, original(worker=True), replicas(api))
            marker = runtime / 'auto-release-enabled'
            with patch.object(release, 'ROOT', root), patch.object(release, 'RUNTIME', runtime), patch.object(release, 'ENABLED', marker), patch.object(release, 'Path', side_effect=lambda value: units if value == '/etc/systemd/system' else Path(value)), patch.object(release, 'Host', return_value=host), patch.object(release, 'owned_route', return_value=True), patch.object(release, 'systemctl') as systemctl:
                result = release.install(100)
            self.assertEqual(result['action'], 'installed')
            self.assertEqual(host.calls, ['snapshot', 'original-ready'])
            self.assertTrue(marker.exists())
            service = (units / (release.UNIT + '.service')).read_text()
            self.assertIn(str(runtime / 'controllers'), service)
            self.assertNotIn(str(root), service)
            self.assertIn('--max-connections 100', service)
            timer = (units / (release.UNIT + '.timer')).read_text()
            self.assertIn('OnUnitInactiveSec=15s', timer)
            pinned = next((runtime / 'controllers').iterdir())
            (root / names[0]).write_text('later unreviewed change')
            self.assertEqual((pinned / names[0]).read_text(), 'synthetic reviewed content')
            systemctl.assert_any_call('enable', '--now', release.UNIT + '.timer')

    def test_refresh_preserves_secret_literals_and_pins_management_image(self):
        with tempfile.TemporaryDirectory() as directory:
            runtime = Path(directory)
            api = original('b', secret='$literal-${EXAMPLE}-"quoted"')
            success = release.subprocess.CompletedProcess([], 0, b'', b'')
            with patch.object(release, 'RUNTIME', runtime), patch.object(release.subprocess, 'run', return_value=success) as run:
                release.Host().refresh(api)
            saved = next((runtime / 'releases').glob('*/runtime.env'))
            self.assertIn('JWT_SECRET=$literal-${EXAMPLE}-"quoted"\n', saved.read_text())
            helper = (runtime / 'manage.sh').read_text()
            self.assertIn(api['Image'], helper)
            self.assertIn(str(saved), helper)
            self.assertEqual(run.call_count, 2)
            self.assertIn('--no-deps', run.call_args_list[1].args[0])
            self.assertEqual(run.call_args_list[1].args[0][-1], 'api')

    def test_failed_refresh_keeps_previous_management_helper(self):
        with tempfile.TemporaryDirectory() as directory:
            runtime = Path(directory)
            helper = runtime / 'manage.sh'
            helper.write_text('previous known-good helper')
            results = [release.subprocess.CompletedProcess([], 0), release.subprocess.CompletedProcess([], 1)]
            with patch.object(release, 'RUNTIME', runtime), patch.object(release.subprocess, 'run', side_effect=results):
                with self.assertRaises(RuntimeError):
                    release.Host().refresh(original('b'))
            self.assertEqual(helper.read_text(), 'previous known-good helper')


if __name__ == '__main__':
    unittest.main()
