"""Preview or apply a reversible API route through the tested replica gateway.

Default is read-only. --execute recreates only the gateway onto the shared
network, writes one new watched Traefik file and verifies local HTTPS routing.
The original API/router stay running. --rollback removes only our matching file.
No cloud resources, DNS changes, proxy restart, migrations or load tests.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('pilot', ROOT / 'scripts/replica-pilot.py')
pilot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pilot)
RUNTIME = Path('/opt/atlxpres-scale-pilot-runtime')
ROUTE = Path('/data/coolify/proxy/dynamic/atlxpres-replica-route.yaml')
STATE = RUNTIME / 'route-state.json'
GATEWAY = 'atlxpres-scale-pilot-gateway-1'
DOMAIN = 'api.atlxpres.com'


def inspect(name, field):
    return json.loads(pilot.docker('inspect', '--format', '{{json .' + field + '}}', name))


def routing(labels, proxy_args):
    options = {}
    for arg in proxy_args:
        if arg.startswith('--') and '=' in arg:
            key, value = arg[2:].split('=', 1)
            options[key.lower()] = value
    if options.get('providers.file.directory', '').rstrip('/') != '/traefik/dynamic':
        raise ValueError('Expected a watched /traefik/dynamic directory; proxy configuration needs review')
    if options.get('providers.file.watch', '').lower() != 'true':
        raise ValueError('Explicit file-provider watch=true is required')
    secure = [key[len('entrypoints.'):-len('.address')] for key, value in options.items()
              if key.startswith('entrypoints.') and key.endswith('.address') and value in {':443', ':443/tcp'}]
    if len(secure) != 1:
        raise ValueError('Expected one HTTPS entry point on port443')
    prefix = 'traefik.http.routers.https-0-' + pilot.API_RESOURCE
    lowered = {key.lower(): value for key, value in labels.items()}
    expected_rule = 'Host(`api.atlxpres.com`) && PathPrefix(`/`)'
    if lowered.get(prefix + '.rule') != expected_rule or lowered.get(prefix + '.tls') != 'true':
        raise ValueError('Live API router does not match reviewed host/TLS rule')
    if lowered.get(prefix + '.tls.certresolver') != 'letsencrypt':
        raise ValueError('Live certificate resolver changed; review required')
    entry = lowered.get(prefix + '.entrypoints', secure[0])
    if entry != secure[0]:
        raise ValueError('Live API entry point differs from HTTPS listener')
    middlewares = lowered.get(prefix + '.middlewares', '').split(',')
    if middlewares == ['gzip-sse']:
        # Preserve the original Docker middleware, including SSE exclusion.
        # A name alone is not evidence of a reviewed compression definition.
        base = 'traefik.http.middlewares.gzip-sse.'
        definition = {key[len(base):]: value for key, value in lowered.items() if key.startswith(base)}
        if definition != {'compress': 'true', 'compress.excludedcontenttypes': 'text/event-stream'}:
            raise ValueError('gzip-sse middleware definition differs from reviewed compression policy')
    elif middlewares != ['gzip']:
        raise ValueError('Live middleware chain changed; preserve it through a separate review')
    priority = lowered.get(prefix + '.priority', '0')
    if not re.fullmatch(r'\d{1,7}', priority):
        raise ValueError('Unexpected existing router priority')
    return {'http': {
        'routers': {'atlxpres-replicas': {
            'entryPoints': [secure[0]], 'rule': expected_rule,
            'priority': max(1000, int(priority) + 1000),
            'middlewares': [middlewares[0] + '@docker'], 'service': 'atlxpres-available',
            'tls': {'certResolver': 'letsencrypt'}}},
        'services': {'atlxpres-available': {'failover': {
            'service': 'atlxpres-replicas',
            'fallback': 'https-0-' + pilot.API_RESOURCE + '@docker'}},
            'atlxpres-replicas': {'loadBalancer': {
            'passHostHeader': True,
            'servers': [{'url': 'http://' + GATEWAY + ':8080'}],
            'healthCheck': {'path': '/api/v1/ready', 'interval': '10s', 'timeout': '3s'}}}}}}


def digest(data):
    return hashlib.sha256(data).hexdigest()


def probe():
    # Reach this host's Traefik with real TLS/SNI, bypassing DNS/CDN caches.
    with tempfile.TemporaryDirectory() as directory:
        headers, body = Path(directory) / 'headers', Path(directory) / 'body'
        result = subprocess.run(['curl', '-fsS', '--max-time', '8', '--connect-timeout', '3',
                                 '--resolve', DOMAIN + ':443:127.0.0.1', '-H', 'Cache-Control: no-cache',
                                 '-D', str(headers), '-o', str(body), '--write-out', '%{http_code}',
                                 'https://' + DOMAIN + '/api/v1/ready'],
                                capture_output=True, timeout=12)
        if result.returncode:
            status = result.stdout.decode(errors='replace').strip()
            status = status if re.fullmatch(r'\d{3}', status) else 'unknown'
            raise RuntimeError('Local HTTPS readiness failed: curl_exit=%d http_status=%s' % (result.returncode, status))
        data = json.loads(body.read_text())
        if data.get('ok') is not True:
            raise ValueError('Local HTTPS dependencies are not ready')
        backend = ''
        for line in headers.read_text().splitlines():
            if line.lower().startswith('x-atl-pilot-backend:'):
                backend = line.split(':', 1)[1].strip()
        return backend


def rollback(pause_updates=True):
    if STATE.is_symlink() or ROUTE.is_symlink():
        raise ValueError('Refusing a symlink in route rollback')
    if not STATE.is_file():
        raise ValueError('No saved route ownership record; manual review required')
    state = json.loads(STATE.read_text())
    if state.get('path') != str(ROUTE):
        raise ValueError('Saved route path differs from scoped target')
    if ROUTE.exists():
        if digest(ROUTE.read_bytes()) != state.get('sha256'):
            raise ValueError('Route changed since activation; refusing to remove someone else\'s changes')
        ROUTE.unlink()
    STATE.unlink()
    if pause_updates:
        # A manual rollback must remain effective if a release timer is installed.
        (RUNTIME / 'auto-release-enabled').unlink(missing_ok=True)
    print('Replica route removed. Existing Coolify API route is retained.')


def helper(*args):
    result = subprocess.run([str(RUNTIME / 'manage.sh'), *args], capture_output=True, timeout=180)
    if result.returncode:
        raise RuntimeError('Gateway update failed; details suppressed to protect runtime settings')


def main(argv=None, _locked=False):
    parser = argparse.ArgumentParser(description=__doc__)
    operation = parser.add_mutually_exclusive_group()
    operation.add_argument('--execute', action='store_true')
    operation.add_argument('--rollback', action='store_true')
    args = parser.parse_args(argv)
    if (args.execute or args.rollback) and (not hasattr(os, 'geteuid') or os.geteuid() != 0):
        raise ValueError('Execution requires root on the main Linux server')
    if (args.execute or args.rollback) and not _locked:
        with pilot.operation_lock(RUNTIME):
            return main(argv, _locked=True)
    if args.rollback:
        rollback()
        return
    apis = pilot.docker('ps', '-q', '--filter', 'name=' + pilot.API_RESOURCE + '-').split()
    if len(apis) != 1:
        raise ValueError('Expected one production API; wait for rolling deployment to finish')
    api = apis[0]
    config = routing(inspect(api, 'Config.Labels') or {}, inspect('coolify-proxy', 'Config.Cmd') or [])
    mounts = inspect('coolify-proxy', 'Mounts')
    if not any(m['Source'] == '/data/coolify/proxy' and m['Destination'] == '/traefik' for m in mounts):
        raise ValueError('Proxy directory mount differs from reviewed configuration')
    if ROUTE.exists() or ROUTE.is_symlink() or STATE.exists():
        raise ValueError('Route or ownership record already exists; inspect or roll back before another activation')
    if not ROUTE.parent.is_dir() or ROUTE.parent.is_symlink():
        raise ValueError('Expected real existing proxy dynamic directory')
    replicas = pilot.docker('ps', '-q', '--filter', 'label=com.docker.compose.project=' + pilot.PROJECT,
                            '--filter', 'label=com.docker.compose.service=api').split()
    if len(replicas) != 2:
        raise ValueError('Expected exactly two pilot APIs')
    image = inspect(api, 'Image')
    for replica in replicas:
        if inspect(replica, 'Image') != image or inspect(replica, 'State.Health.Status') != 'healthy':
            raise ValueError('Pilot API images/health differ from the production API; refresh before routing')
    # No cutover while local HTTPS is already using a replica route.
    if probe():
        raise ValueError('Local HTTPS already has a pilot backend header; review existing routing')
    content = (json.dumps(config, indent=2) + '\n').encode()
    print('Proposed file:', ROUTE)
    print(content.decode())
    if not args.execute:
        print('Preview only: no files, networks or routes changed.')
        return
    runtime_stat = RUNTIME.stat()
    if RUNTIME.is_symlink() or runtime_stat.st_uid != 0 or runtime_stat.st_mode & 0o077:
        raise ValueError('Runtime directory must be root-owned and private')
    # Gateway only; API and worker containers are not recreated.
    helper('up', '-d', '--no-deps', 'gateway', '--wait', '--wait-timeout', '90')
    if 'coolify' not in inspect(GATEWAY, 'NetworkSettings.Networks'):
        raise ValueError('Gateway did not join the shared coolify network')
    state = {'path': str(ROUTE), 'sha256': digest(content)}
    descriptor = os.open(STATE, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'w') as stream:
        json.dump(state, stream)
    # The file is dedicated to this route. O_EXCL never overwrites Coolify config.
    installed = False
    try:
        # Publish atomically under the watched parent mount, avoiding partial YAML.
        with tempfile.NamedTemporaryFile(dir=ROUTE.parent, prefix='.atl-route-', suffix='.tmp', delete=False) as stream:
            pending = Path(stream.name)
            stream.write(content)
            os.chmod(pending, 0o644)
        try:
            os.link(pending, ROUTE)
            installed = True
        finally:
            pending.unlink()
        seen = set()
        outcomes = {}
        deadline = time.monotonic() + 45
        for _ in range(20):
            if time.monotonic() >= deadline:
                break
            time.sleep(1)
            try:
                backend = probe()
                outcome = backend if backend in {'api1', 'api2', 'api3', 'api4'} else 'original/fallback (no replica header)' if not backend else 'unexpected replica header'
                outcomes[outcome] = outcomes.get(outcome, 0) + 1
                if outcomes[outcome] == 1:
                    print('HTTPS verification:', outcome, flush=True)
                if backend in {'api1', 'api2', 'api3', 'api4'}:
                    seen.add(backend)
                    print('HTTPS readiness via', backend)
                if len(seen) >= 2:
                    print('Live HTTPS route verified through two replica slots.')
                    print('Now test customer/provider chat, uploads, Support and Town Hall on devices.')
                    print('Rollback: python3 scripts/replica-route.py --rollback')
                    return
            except (ValueError, RuntimeError, subprocess.TimeoutExpired, json.JSONDecodeError) as error:
                outcome = str(error) if type(error) is RuntimeError else type(error).__name__
                outcomes[outcome] = outcomes.get(outcome, 0) + 1
                if outcomes[outcome] == 1:
                    print('HTTPS verification:', outcome, flush=True)
                continue
        print('Verification outcomes:', json.dumps(outcomes), flush=True)
        raise RuntimeError('Two healthy replica paths were not confirmed; reverting the new route')
    except BaseException:
        if installed:
            rollback()
        else:
            STATE.unlink(missing_ok=True)
        raise


if __name__ == '__main__':
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, interrupted)
    try:
        main()
    except (ValueError, RuntimeError) as error:
        print('STOPPED:', str(error), file=sys.stderr)
        sys.exit(1)
    except (OSError, subprocess.TimeoutExpired, KeyError, json.JSONDecodeError, KeyboardInterrupt) as error:
        print('STOPPED:', type(error).__name__, '(details suppressed)', file=sys.stderr)
        sys.exit(1)
