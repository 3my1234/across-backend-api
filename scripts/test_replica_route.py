import copy
import importlib.util
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
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('route', Path(__file__).with_name('replica-route.py'))
route = importlib.util.module_from_spec(spec)
spec.loader.exec_module(route)
PREFIX = 'traefik.http.routers.https-0-' + route.pilot.API_RESOURCE
LABELS = {PREFIX + '.rule': 'Host(`api.atlxpres.com`) && PathPrefix(`/`)',
          PREFIX + '.tls': 'true', PREFIX + '.tls.certresolver': 'letsencrypt',
          PREFIX + '.middlewares': 'gzip', PREFIX + '.entryPoints': 'https'}
ARGS = ['--providers.file.directory=/traefik/dynamic/', '--providers.file.watch=true',
        '--entrypoints.http.address=:80', '--entrypoints.https.address=:443']


class RouteSafety(unittest.TestCase):
    def test_route_preserves_tls_and_middleware_and_fallback(self):
        config = route.routing(LABELS, ARGS)['http']
        router = config['routers']['atlxpres-replicas']
        self.assertEqual(router['entryPoints'], ['https'])
        self.assertEqual(router['middlewares'], ['gzip@docker'])
        self.assertEqual(router['tls'], {'certResolver': 'letsencrypt'})
        self.assertEqual(router['priority'], 1000)
        failover = config['services']['atlxpres-available']['failover']
        self.assertTrue(failover['fallback'].endswith('@docker'))
        self.assertNotIn('errors', failover, 'never replay an ambiguous write on fallback')
        self.assertNotIn('retry', json.dumps(config).lower())

    def test_drift_stops_before_writing(self):
        for key, value in ((PREFIX + '.rule', 'Host(`other.test`)'),
                           (PREFIX + '.middlewares', 'authentication,gzip'),
                           (PREFIX + '.tls.certresolver', 'changed')):
            with self.assertRaises(ValueError):
                route.routing(dict(LABELS, **{key: value}), ARGS)
        with self.assertRaises(ValueError):
            route.routing(LABELS, [arg for arg in ARGS if 'watch' not in arg])

    def test_rollback_removes_only_owned_matching_file(self):
        with tempfile.TemporaryDirectory() as directory:
            target, state = Path(directory) / 'route.yaml', Path(directory) / 'state.json'
            original = b'{"http":{}}'
            target.write_bytes(original)
            state.write_text(json.dumps({'path': str(target), 'sha256': route.digest(original)}))
            with patch.object(route, 'ROUTE', target), patch.object(route, 'STATE', state):
                target.write_text('changed-by-another-operator')
                with self.assertRaises(ValueError):
                    route.rollback()
                self.assertTrue(target.exists())
                target.write_bytes(original)
                route.rollback()
                self.assertFalse(target.exists())
                self.assertFalse(state.exists())

    @unittest.skipUnless(shutil.which('docker'), 'Real Traefik failover runs in Linux CI')
    def test_traefik36_routes_and_falls_back_without_restarting_proxy(self):
        project = 'route-validation-' + uuid.uuid4().hex[:10]
        primary, backup, proxy = [project + '-' + name for name in ('primary', 'backup', 'proxy')]
        names = [primary, backup, proxy]
        def docker(*args):
            result = subprocess.run(['docker', *args], capture_output=True, text=True, timeout=180)
            self.assertEqual(result.returncode, 0, 'Synthetic Docker test failed: ' + result.stderr)
            return result.stdout.strip()
        with tempfile.TemporaryDirectory() as directory:
            config = copy.deepcopy(route.routing(LABELS, ARGS))
            router = config['http']['routers']['atlxpres-replicas']
            router.pop('tls'); router.pop('middlewares')
            router['entryPoints'], router['rule'] = ['http'], 'Host(`pilot.test`)'
            services = config['http']['services']
            services['atlxpres-available']['failover']['fallback'] = 'backup'
            services['backup'] = {'loadBalancer': {'servers': [{'url': 'http://' + backup + ':8080'}]}}
            main = services['atlxpres-replicas']['loadBalancer']
            main['servers'] = [{'url': 'http://' + primary + ':8080'}]
            main['healthCheck']['interval'], main['healthCheck']['timeout'] = '1s', '500ms'
            (Path(directory) / 'route.yaml').write_text(json.dumps(config))
            docker('network', 'create', project)
            try:
                for name, body in ((primary, 'PRIMARY'), (backup, 'FALLBACK')):
                    docker('run', '-d', '--name', name, '--network', project, 'alpine:3.20', 'sh', '-c',
                           'mkdir -p /www/api/v1; echo ' + body + ' > /www/api/v1/ready; exec httpd -f -p 8080 -h /www')
                docker('run', '-d', '--name', proxy, '--network', project, '-p', '127.0.0.1::8000',
                       '-v', str(Path(directory).resolve()) + ':/dynamic:ro', 'traefik:v3.6',
                       '--entrypoints.http.address=:8000', '--providers.file.directory=/dynamic', '--providers.file.watch=true')
                ports = json.loads(docker('inspect', '--format', '{{json .NetworkSettings.Ports}}', proxy))
                port = ports['8000/tcp'][0]['HostPort']
                def wait_for(body):
                    for _ in range(50):
                        try:
                            request = urllib.request.Request('http://127.0.0.1:' + port + '/api/v1/ready', headers={'Host': 'pilot.test'})
                            with urllib.request.urlopen(request, timeout=2) as response:
                                if response.read().decode().strip() == body:
                                    return
                        except Exception:
                            pass
                        time.sleep(0.2)
                    self.fail('Traefik did not reach expected synthetic backend ' + body)
                wait_for('PRIMARY')
                docker('stop', '--time', '2', primary)
                wait_for('FALLBACK')
                docker('start', primary)
                wait_for('PRIMARY')
            finally:
                for name in names:
                    subprocess.run(['docker', 'rm', '-f', name], capture_output=True, timeout=30)
                subprocess.run(['docker', 'network', 'rm', project], capture_output=True, timeout=30)


if __name__ == '__main__':
    unittest.main()
