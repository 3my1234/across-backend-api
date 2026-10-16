import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import shutil
import tempfile
import unittest
import uuid
from contextlib import redirect_stdout
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('pilot', Path(__file__).with_name('replica-pilot.py'))
pilot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pilot)


def container(**env):
    return {'Config': {'Env': [key + '=' + value for key, value in env.items()]}}


class PilotSafety(unittest.TestCase):
    @unittest.skipUnless(shutil.which('docker'), 'Docker Compose checked in Linux CI')
    def test_compose_preserves_runtime_literals_and_private_binding(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'runtime.env'
            secret = '$literal-${UNSET}-"quote"=value'
            pilot.private_env(target, {'SECRET': secret, 'DATABASE_READ_URL': 'must-be-overridden'})
            project = 'pilot-validation-' + uuid.uuid4().hex[:12]
            env = dict(os.environ, ATL_API_IMAGE='alpine:3.20', ATL_RUNTIME_ENV=str(target), ATL_SHARED_NETWORK=project)
            compose = Path(__file__).parents[1] / 'deploy/scaling/compose.yml'
            result = pilot.subprocess.run(['docker', 'compose', '-f', str(compose), 'config', '--format', 'json'], capture_output=True, text=True, env=env)
            self.assertEqual(result.returncode, 0, 'Compose configuration failed')
            config = json.loads(result.stdout)
            api = config['services']['api']
            # Compose serializes literal dollars as $$ in rendered config. Verify
            # the actual container environment below, not that serialization.
            self.assertEqual(api['environment']['DATABASE_READ_URL'], '')
            self.assertEqual(api['environment']['DB_MAX_CONNS'], '8')
            self.assertIn('atlxpres-pilot-api', api['networks']['default']['aliases'])
            self.assertNotIn('atlxpres-pilot-api', config['services']['gateway']['networks']['default'].get('aliases', []))
            self.assertFalse(api.get('ports'))
            self.assertEqual(config['services']['gateway']['ports'][0]['host_ip'], '127.0.0.1')
            command = ['docker', 'compose', '-p', project, '-f', str(compose)]
            pilot.docker('network', 'create', project)
            try:
                import shlex
                run = pilot.subprocess.run(command + ['run', '--rm', '--no-deps', '-T', '--entrypoint', 'sh', 'api', '-c', 'test "$SECRET" = ' + shlex.quote(secret) + ' && test -z "$DATABASE_READ_URL"'], env=env, capture_output=True, text=True, timeout=180)
                self.assertEqual(run.returncode, 0, 'Synthetic container env check failed: ' + run.stderr)
            finally:
                pilot.subprocess.run(command + ['down'], env=env, capture_output=True, timeout=60)
                pilot.docker('network', 'rm', project)

    def test_default_preflight_performs_no_startup_or_file_writes(self):
        commit = 'a' * 40
        configs = []
        for resource in (pilot.API_RESOURCE, pilot.WORKER_RESOURCE):
            configs.append(dict(Name='/' + resource + '-running', Id=resource,
                                Config=dict(Image='local:' + commit, Labels=None,
                                            Env=['RUN_INLINE_WORKERS=false', 'REDIS_URL=private-do-not-print']),
                                State=dict(Health=dict(Status='healthy')),
                                NetworkSettings=dict(Networks=dict(coolify={}))))
        def run(*args):
            if args[0] == 'ps':
                if '--filter' in args:
                    return ''
                return '\n'.join(json.dumps({'Names': c['Name'].lstrip('/'), 'ID': c['Id']}) for c in configs)
            if args[0] == 'inspect':
                return json.dumps(configs)
            if args[0] == 'exec':
                return '{"ok":true}'
            if args == ('compose', 'version'):
                return 'Compose v5.3.0'
            self.fail('Unexpected operation: ' + args[0])
        output = io.StringIO()
        with patch.object(pilot.sys, 'argv', ['pilot', '--expected-commit', commit, '--max-connections', '100']), patch.object(pilot, 'docker', side_effect=run), patch.object(pilot.os, 'open', side_effect=AssertionError('must not write')), redirect_stdout(output):
            pilot.main()
        self.assertIn('94/100', output.getvalue())
        self.assertIn('Preflight only', output.getvalue())
        self.assertNotIn('private-do-not-print', output.getvalue())

    def test_budget_counts_current_pools_and_rolling_surge(self):
        self.assertEqual(pilot.connection_budget([container(), container()], 100), (40, 94))
        with self.assertRaises(ValueError):
            pilot.connection_budget([container(DATABASE_READ_URL='private'), container()], 100)
        with self.assertRaises(ValueError):
            pilot.connection_budget([container(DB_MAX_CONNS='not-a-number')], 100)

    def test_private_values_preserve_literal_dollars_and_quotes(self):
        secret = 'literal-$value-${EXAMPLE}-"quoted"-with=equals'
        env = pilot.environment(container(SECRET=secret, HOSTNAME='old'))
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'runtime.env'
            pilot.private_env(target, env)
            self.assertEqual(target.read_text(), 'SECRET=' + secret + '\n')
            if os.name != 'nt':
                self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)
            with self.assertRaises(FileExistsError):
                pilot.private_env(target, env)

    def test_multiline_and_invalid_keys_stop_without_echoing_secrets(self):
        for env in ({'SECRET': 'sensitive\nvalue'}, {'INVALID-KEY': 'sensitive'}):
            with self.assertRaises(ValueError) as error:
                pilot.environment(container(**env))
            self.assertNotIn('sensitive', str(error.exception))

    def test_docker_failure_does_not_echo_resolved_settings(self):
        with patch.object(pilot.subprocess, 'run', return_value=pilot.subprocess.CompletedProcess([], 1, 'private', 'private')):
            with self.assertRaises(RuntimeError) as error:
                pilot.docker('inspect', 'example')
            self.assertNotIn('private', str(error.exception))

    def test_pilot_has_no_production_route_and_bounded_single_pool(self):
        source = (Path(__file__).parents[1] / 'deploy/scaling/compose.yml').read_text()
        self.assertIn('127.0.0.1:18080:8080', source)
        self.assertIn('format: raw', source)
        self.assertIn('DATABASE_READ_URL: ""', source)
        self.assertNotIn('traefik.', source)
        self.assertIn('RUN_INLINE_WORKERS: "false"', source)
        self.assertIn('RUN_MIGRATIONS: "false"', source)


if __name__ == '__main__':
    unittest.main()
