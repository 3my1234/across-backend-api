import hashlib
import importlib.util
import json
import os
import shlex
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('transition', Path(__file__).with_name('realtime-controller-transition.py'))
transition = importlib.util.module_from_spec(spec)
spec.loader.exec_module(transition)

class ControllerTransition(unittest.TestCase):
    def scenario(self, bad_gateway=False, rollback_fail=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            runtime = root / 'runtime'; runtime.mkdir()
            oldpin = runtime / 'controllers' / ('a' * 16)
            oldcompose = oldpin / 'deploy/scaling/compose.yml'; oldcompose.parent.mkdir(parents=True)
            oldcompose.write_text('old compose')
            (oldcompose.parent / 'haproxy.cfg').write_text('old gateway')
            envfile = runtime / 'releases' / 'old' / 'runtime.env'; envfile.parent.mkdir(parents=True); envfile.write_text('PRIVATE')
            image = 'sha256:' + 'b' * 64
            helper = runtime / 'manage.sh'
            oldhelper = ('#!/bin/sh\nexport ATL_API_IMAGE=' + image + '\nexport ATL_RUNTIME_ENV=' + shlex.quote(str(envfile)) +
                         '\nexport ATL_SHARED_NETWORK=coolify\nexec docker compose -f ' + shlex.quote(str(oldcompose)) + ' "$@"\n')
            helper.write_text(oldhelper)
            service, timer = root / 'service', root / 'timer'
            oldservice = '# Managed by Atl xpres replica-release.py\nold service\n'
            service.write_bytes(oldservice.encode()); timer.write_bytes(b'# Managed by Atl xpres replica-release.py\nold timer\n')
            enabled = runtime / 'auto-release-enabled'; enabled.write_text('enabled\n')
            route_file, route_state = root / 'route.yaml', root / 'route-state.json'
            route_file.write_text('old route'); route_state.write_text('old state')
            active = {'route': True, 'activate': 0, 'fallback': 0, 'inspect': 0}
            class Host:
                def snapshot(self): return ({'Image': image}, {}, [])
                def original_ready(self, api): pass
                def fallback(self): active['route'] = False; active['fallback'] += 1
                def activate(self):
                    active['activate'] += 1
                    if rollback_fail: raise RuntimeError('synthetic gateway failure')
                    active['route'] = True
            signature = hashlib.sha256(b''.join((transition.ROOT / n).read_bytes() for n in transition.files)).hexdigest()[:16]
            newpin = runtime / 'controllers' / signature
            def install(_):
                newconfig = newpin / 'deploy/scaling/haproxy.cfg'; newconfig.parent.mkdir(parents=True)
                newconfig.write_bytes((transition.ROOT / 'deploy/scaling/haproxy.cfg').read_bytes())
                service.write_text('# Managed by Atl xpres replica-release.py\nnew service\n')
            def inspect(*_):
                active['inspect'] += 1
                path = oldcompose.parent / 'haproxy.cfg' if active['inspect'] == 1 else newpin / 'deploy/scaling/haproxy.cfg'
                if bad_gateway and active['inspect'] > 1: path = oldcompose.parent / 'haproxy.cfg'
                return json.dumps([{'Mounts': [{'Source': str(path), 'Destination': '/usr/local/etc/haproxy/haproxy.cfg'}]}])
            def process(args, **kwargs):
                output = str(oldpin / 'scripts/replica-release.py') if args[:2] == ['systemctl', 'show'] else ''
                return type('Result', (), {'returncode': 0, 'stdout': output})()
            read = lambda path: path.read_bytes()
            with patch.object(transition.os, 'geteuid', return_value=0, create=True), patch.object(transition, 'runtime', runtime), patch.object(transition, 'service', service), patch.object(transition, 'timer', timer), patch.object(transition, 'helper', helper), patch.object(transition, 'require_file', side_effect=read), patch.object(transition.release, 'ENABLED', enabled), patch.object(transition.release.route, 'ROUTE', route_file), patch.object(transition.release.route, 'STATE', route_state), patch.object(transition.release, 'Host', return_value=Host()), patch.object(transition.release, 'decide', return_value={'action': 'aligned', 'commit': 'c'*40}), patch.object(transition.release, 'owned_route', side_effect=lambda: active['route']), patch.object(transition.release, 'install', side_effect=install), patch.object(transition.release, 'systemctl'), patch.object(transition.release.pilot, 'docker', side_effect=inspect), patch.object(transition.subprocess, 'run', side_effect=process):
                if bad_gateway or rollback_fail:
                    with self.assertRaisesRegex(RuntimeError, 'synthetic gateway failure' if rollback_fail else 'does not mount'): transition.run(True)
                    self.assertEqual(helper.read_text(), oldhelper)
                    self.assertEqual(service.read_text(), oldservice)
                    self.assertEqual(enabled.is_file(), not rollback_fail)
                    self.assertEqual(active['activate'], 2)
                else:
                    transition.run(True)
                    self.assertIn(str(newpin / 'deploy/scaling/compose.yml'), helper.read_text())
                    self.assertEqual(active['activate'], 1)
                self.assertEqual(active['route'], not rollback_fail)
                self.assertGreaterEqual(active['fallback'], 1)
                self.assertEqual(len(list(runtime.glob('realtime-controller-backup-*'))), 1)

    def test_transition_uses_new_pinned_gateway(self): self.scenario()
    def test_gateway_mismatch_restores_old_controller_and_route(self): self.scenario(True)
    def test_failed_rollback_leaves_original_fallback_and_disables_timer(self): self.scenario(rollback_fail=True)

if __name__ == '__main__': unittest.main()
