"""Disposable CI-only regression for gateway private -> shared network change."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest
import urllib.request
import uuid


@unittest.skipUnless(shutil.which('docker'), 'Docker discovery regression runs in Linux CI')
class ReplicaDiscovery(unittest.TestCase):
    def test_dual_stack_shared_network_keeps_two_ipv4_backends(self):
        project = 'discovery-' + uuid.uuid4().hex[:10]
        shared, decoy = project + '-shared', project + '-decoy'
        root = Path(__file__).parents[1]
        compose = root / 'deploy/scaling/compose.yml'
        def command(args, env=None):
            result = subprocess.run(args, capture_output=True, text=True, env=env, timeout=180)
            self.assertEqual(result.returncode, 0, 'Synthetic discovery operation failed: ' + result.stderr)
            return result.stdout.strip()
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            runtime = directory / 'runtime.env'
            runtime.write_text('FIXTURE_ONLY=true\n')
            # Application listens only on IPv4, matching the live Fiber listener.
            fixture = {'services': {'api': {
                'command': ['sh', '-c', 'mkdir -p /www/api/v1; printf "{\\"ok\\":true,\\"instance\\":\\"%s\\"}\\n" "$$HOSTNAME" > /www/api/v1/ready; exec httpd -f -p 0.0.0.0:8080 -h /www'],
                'healthcheck': {'test': ['CMD', 'wget', '-qO-', 'http://127.0.0.1:8080/api/v1/ready'],
                                'interval': '1s', 'timeout': '2s', 'retries': 10, 'start_period': '1s'}}}}
            fixture_file = directory / 'fixture.json'
            fixture_file.write_text(json.dumps(fixture))
            private_gateway = directory / 'private-gateway.yaml'
            private_gateway.write_text('services:\n  gateway:\n    networks: !override [default]\n')
            env = dict(os.environ, ATL_API_IMAGE='busybox:1.36', ATL_RUNTIME_ENV=str(runtime), ATL_SHARED_NETWORK=shared)
            base = ['docker', 'compose', '-p', project, '-f', str(compose), '-f', str(fixture_file)]
            subnet = 'fd99:' + uuid.uuid4().hex[:4] + ':' + uuid.uuid4().hex[:4] + '::/64'
            command(['docker', 'network', 'create', '--ipv6', '--subnet', subnet, shared])
            try:
                # Generic api alias on shared network is deliberately unrelated.
                command(['docker', 'run', '-d', '--name', decoy, '--network', shared, '--network-alias', 'api',
                         'busybox:1.36', 'sleep', '300'])
                command(base + ['-f', str(private_gateway), 'up', '-d', '--scale', 'api=2', '--wait', '--wait-timeout', '60'], env)
                def verify_two():
                    seen, errors = set(), []
                    for _ in range(60):
                        try:
                            with urllib.request.urlopen('http://127.0.0.1:18080/api/v1/ready', timeout=3) as response:
                                data = json.load(response)
                                self.assertTrue(data['ok'])
                                seen.add(data['instance'])
                            if len(seen) == 2:
                                return
                        except Exception as error:
                            errors.append(type(error).__name__)
                        time.sleep(0.2)
                    logs = subprocess.run(base + ['logs', '--tail', '35', 'gateway'], env=env, capture_output=True, text=True, timeout=15)
                    self.fail('Two synthetic backends not observed: ' + repr(errors[-3:]) + '\n' + logs.stdout + logs.stderr)
                verify_two()
                # Same gateway-only update performed by the route activation tool.
                command(base + ['up', '-d', '--no-deps', 'gateway', '--wait', '--wait-timeout', '60'], env)
                verify_two()
                print('Dual-stack transition passed: both IPv4 APIs answer before/after gateway joins shared IPv6 network.')
            finally:
                subprocess.run(base + ['down'], env=env, capture_output=True, timeout=90)
                subprocess.run(['docker', 'rm', '-f', decoy], capture_output=True, timeout=15)
                subprocess.run(['docker', 'network', 'rm', shared], capture_output=True, timeout=15)


if __name__ == '__main__':
    unittest.main()
