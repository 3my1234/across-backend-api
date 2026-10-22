"""Small exact-version proxy-chain functional test, CI only; no production IO."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import uuid


def run(*args, timeout=40):
    value = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
    if value.returncode:
        raise RuntimeError('isolated fixture command failed: '+args[0])
    return value.stdout.strip()


def main():
    if os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('ATL_INGRESS_LAB') != '1':
        raise SystemExit('STOPPED: isolated CI fixture only')
    binary = Path(os.environ['INGRESS_LAB_BINARY']).resolve(strict=True)
    if binary.read_bytes()[:4] != b'\x7fELF':
        raise SystemExit('STOPPED: reviewed Linux fixture binary required')
    owner = 'atl-ingress-'+uuid.uuid4().hex[:12]
    containers = []
    network_created = False
    results = []
    def docker(*args): return run('docker', *args)
    try:
        docker('network', 'create', '--internal', '--subnet', '10.244.89.0/24', '--label', 'atl.ingress='+owner, owner)
        network_created = True
        with tempfile.TemporaryDirectory(prefix='atl-ingress-') as directory:
            root = Path(directory)
            root.chmod(0o755)
            (root/'probe').write_bytes(binary.read_bytes())
            (root/'probe').chmod(0o555)
            routes = 'http:\n  routers:\n    fixture:\n      rule: Host(`api.staging.invalid`)\n      service: fixture\n  services:\n    fixture:\n      loadBalancer:\n        servers:\n          - url: http://10.244.89.3:8080\n'
            (root/'routes.yml').write_text(routes)
            (root/'routes.yml').chmod(0o644)
            def start(role, ip, image, command):
                name = owner+'-'+role
                docker('run', '-d', '--pull=never', '--name', name, '--label', 'atl.ingress='+owner,
                       '--network', owner, '--ip', ip, '--user', '65534', '--cap-drop', 'ALL',
                       '--security-opt', 'no-new-privileges', '--memory', '128m', '--cpus', '0.5',
                       '--pids-limit', '64', '--read-only', '--tmpfs', '/tmp:rw,size=16777216',
                       '--mount', 'type=bind,src='+str(root)+',dst=/fixture,readonly',
                       '-e', 'APP_ENV=test', '-e', 'ATL_INGRESS_LAB=1', image, *command)
                containers.append(name)
                return name
            def remove(name):
                if docker('inspect', '--format', '{{index .Config.Labels "atl.ingress"}}', name) != owner:
                    raise RuntimeError('owned fixture container mismatch')
                docker('rm', '-f', '-v', name)
                containers.remove(name)
            def probe(source, target, xff='', cf=''):
                # Short lived probe has no host port, credentials or external URL.
                name = owner+'-probe-'+uuid.uuid4().hex[:8]
                containers.append(name)
                try:
                    raw = docker('run', '--pull=never', '--name', name, '--label', 'atl.ingress='+owner,
                                 '--network', owner, '--ip', source, '--user', '65534', '--cap-drop', 'ALL',
                                 '--security-opt', 'no-new-privileges', '--memory', '64m', '--cpus', '0.25',
                                 '--pids-limit', '32', '--read-only', '--mount', 'type=bind,src='+str(root)+',dst=/fixture,readonly',
                                 '-e', 'APP_ENV=test', '-e', 'ATL_INGRESS_LAB=1', 'alpine:3.23',
                                 '/fixture/probe', '--probe='+target, '--xff='+xff, '--cf='+cf)
                finally:
                    remove(name)
                return json.loads(raw)
            start('api', '10.244.89.4', 'alpine:3.23', ['/fixture/probe'])
            start('traefik', '10.244.89.2', 'traefik:v3.6.25', ['--api.dashboard=false', '--entrypoints.web.address=:8080',
                  '--entrypoints.web.forwardedheaders.trustedips=10.244.89.7/32', '--providers.file.filename=/fixture/routes.yml'])
            original = (Path(__file__).resolve().parents[1]/'deploy/scaling/haproxy.cfg').read_text()
            # Preserve production's HTTP forwarding behavior and timeouts, with
            # only its service-discovery backend replaced by the fixed fixture.
            lines = original.splitlines()
            baseline = '\n'.join('  server fixture 10.244.89.4:8080 check' if line.strip().startswith('server-template ') else line for line in lines)+'\n'
            for append in (False, True):
                content = baseline
                if append:
                    content = content.replace('  default_backend replicas', '  option forwardfor\n  default_backend replicas')
                (root/'haproxy.cfg').write_text(content)
                (root/'haproxy.cfg').chmod(0o644)
                gateway = start('gateway', '10.244.89.3', 'haproxy:3.0-alpine', ['haproxy', '-f', '/fixture/haproxy.cfg'])
                for attempt in range(15):
                    try:
                        probe('10.244.89.5', '10.244.89.2:8080')
                        break
                    except RuntimeError:
                        time.sleep(0.3)
                else:
                    raise RuntimeError('isolated proxy chain did not become ready')
                for name, source, target, xff, cf, expected in (
                    ('untrusted_edge_spoof', '10.244.89.5', '10.244.89.2:8080', '198.51.100.250', '198.51.100.250', '10.244.89.5'),
                    ('direct_gateway_spoof', '10.244.89.6', '10.244.89.3:8080', '198.51.100.250', '', '10.244.89.6' if append else '198.51.100.250'),
                    ('direct_api_spoof', '10.244.89.6', '10.244.89.4:8080', '198.51.100.250', '', '10.244.89.6'),
                    ('trusted_edge_client', '10.244.89.7', '10.244.89.2:8080', '198.51.100.77', '', '198.51.100.77'),
                ):
                    value = probe(source, target, xff, cf)
                    if value.get('verified') != expected:
                        raise RuntimeError('identity assertion failed: '+name)
                    results.append({'mode': 'append' if append else 'current_source', 'case': name,
                                    'expected_verified_ip': expected, 'observed': value, 'passed': True})
                remove(gateway)
        report = {'passed': True, 'cases': results, 'scope': 'synthetic HTTP header chain only; not production topology, Cloudflare, HTTPS, hotspot user limits, capacity or deployment proof'}
    finally:
        for name in list(reversed(containers)):
            if docker('inspect', '--format', '{{index .Config.Labels "atl.ingress"}}', name) == owner:
                docker('rm', '-f', '-v', name)
        if network_created and docker('network', 'inspect', '--format', '{{index .Labels "atl.ingress"}}', owner) == owner:
            docker('network', 'rm', owner)
    report['owned_cleanup_complete'] = True
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
