"""Keep existing replicas consistent with the healthy Coolify API/worker release.

Read-only by default. Installation adds a bounded host timer, not capacity or
cloud resources. Never runs migrations, starts workers or replays HTTP writes.
Refresh failure leaves traffic on the original API and pauses automation.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('route', ROOT / 'scripts/replica-route.py')
route = importlib.util.module_from_spec(spec)
spec.loader.exec_module(route)
pilot = route.pilot
RUNTIME = route.RUNTIME
ENABLED = RUNTIME / 'auto-release-enabled'
OVERRIDES = {'HTTP_ADDR': ':8080', 'RUN_MIGRATIONS': 'false', 'RUN_INLINE_WORKERS': 'false',
             'REDIS_OPTIONAL': 'false', 'DB_MAX_CONNS': '8', 'DB_MIN_CONNS': '1',
             'DATABASE_READ_URL': '', 'HTTP_MAX_IN_FLIGHT': '64'}
EXCLUDED = {'HOSTNAME', 'PATH', 'HOME'}
STORAGE_KEYS = {'DATABASE_URL', 'DB_HOST', 'DB_PORT', 'DB_USERNAME', 'DB_PASSWORD', 'DB_NAME',
                'REDIS_URL', 'REDIS_ADDR', 'REDIS_PASSWORD', 'REDIS_DB'}
UNIT = 'atlxpres-replica-release'


def normalized(env, desired=False):
    values = {key: value for key, value in env.items() if key not in EXCLUDED}
    if desired:
        values.update(OVERRIDES)
    return values


def commit(container):
    match = re.search(r':([a-f0-9]{40})$', container['Config']['Image'])
    if not match:
        raise ValueError('API/worker must use immutable commit-tagged images')
    return match[1]


def healthy(container):
    return container['State'].get('Health', {}).get('Status') == 'healthy'


def decide(api, worker, replicas, maximum):
    """Pure release policy; no connection count scales with user count."""
    if not healthy(api):
        return {'action': 'wait-api', 'reason': 'Original API is not healthy'}
    target = commit(api)
    if not healthy(worker) or commit(worker) != target:
        return {'action': 'wait-worker', 'commit': target, 'reason': 'Waiting for matching healthy worker'}
    env = pilot.environment(api)
    worker_env = pilot.environment(worker)
    if any(env.get(key, '') != worker_env.get(key, '') for key in STORAGE_KEYS):
        return {'action': 'wait-worker', 'commit': target, 'reason': 'API and worker storage settings differ; align them before refreshing'}
    if env.get('RUN_INLINE_WORKERS', '').lower() != 'false':
        raise ValueError('Original API inline workers must explicitly be false')
    if not env.get('REDIS_URL') and not env.get('REDIS_ADDR'):
        raise ValueError('Shared Redis configuration is required')
    if len(replicas) > 2:
        raise ValueError('Unexpected replica count; automatic resizing is not enabled')
    _, required = pilot.connection_budget([api, worker], maximum)
    expected = normalized(env, desired=True)
    aligned = len(replicas) == 2 and all(
        healthy(replica) and replica['Image'] == api['Image'] and
        normalized(pilot.environment(replica)) == expected for replica in replicas)
    return {'action': 'aligned' if aligned else 'refresh', 'commit': target,
            'connection_budget': required, 'maximum_connections': maximum,
            'replicas': 2, 'pool_per_replica': 8}


def owned_route():
    if route.ROUTE.is_symlink() or route.STATE.is_symlink():
        raise ValueError('Route/state must not be symlinks')
    if not route.ROUTE.exists() and not route.STATE.exists():
        return False
    if not route.ROUTE.is_file() or not route.STATE.is_file():
        raise ValueError('Incomplete route ownership record; review required')
    state = json.loads(route.STATE.read_text())
    if state.get('path') != str(route.ROUTE) or state.get('sha256') != route.digest(route.ROUTE.read_bytes()):
        raise ValueError('Route changed outside the release controller; refusing to replace it')
    return True


def atomic_private(path, content, mode=0o600):
    if path.is_symlink():
        raise ValueError('Refusing to replace a symlink')
    with tempfile.NamedTemporaryFile(dir=path.parent, prefix='.release-', delete=False) as stream:
        pending = Path(stream.name)
        try:
            os.chmod(pending, mode)
            stream.write(content.encode())
            stream.flush()
            os.fsync(stream.fileno())
        except BaseException:
            pending.unlink(missing_ok=True)
            raise
    try:
        os.replace(pending, path)
    finally:
        pending.unlink(missing_ok=True)


class Host:
    def snapshot(self):
        selected = []
        for resource in (pilot.API_RESOURCE, pilot.WORKER_RESOURCE):
            ids = pilot.docker('ps', '-q', '--filter', 'name=' + resource + '-').split()
            if len(ids) != 1:
                raise ValueError('Wait for one original API and one worker; rolling deployment still in progress')
            selected.append(json.loads(pilot.docker('inspect', ids[0]))[0])
        ids = pilot.docker('ps', '-a', '-q', '--filter', 'label=com.docker.compose.project=' + pilot.PROJECT,
                           '--filter', 'label=com.docker.compose.service=api').split()
        replicas = json.loads(pilot.docker('inspect', *ids)) if ids else []
        return *selected, replicas

    def original_ready(self, api):
        value = json.loads(pilot.docker('exec', api['Id'], 'curl', '-fsS', '--max-time', '5',
                                       'http://127.0.0.1:8080/api/v1/ready'))
        if value.get('ok') is not True:
            raise ValueError('Original API dependencies are not ready; keeping existing replica route')

    def fallback(self):
        if owned_route():
            route.rollback(pause_updates=False)
            # Confirm file watcher has returned traffic to the original router.
            for _ in range(15):
                time.sleep(1)
                if not route.probe():
                    return
            raise RuntimeError('Original HTTPS route was not confirmed after replica route removal')

    def refresh(self, api):
        releases = RUNTIME / 'releases'
        if releases.is_symlink():
            raise ValueError('Release directory must not be a symlink')
        releases.mkdir(mode=0o700, exist_ok=True)
        destination = releases / (commit(api)[:12] + '-' + uuid.uuid4().hex[:12])
        destination.mkdir(mode=0o700)
        runtime = destination / 'runtime.env'
        pilot.private_env(runtime, pilot.environment(api))
        env = dict(os.environ, ATL_API_IMAGE=api['Image'], ATL_RUNTIME_ENV=str(runtime), ATL_SHARED_NETWORK='coolify')
        compose = ROOT / 'deploy/scaling/compose.yml'
        command = ['docker', 'compose', '-f', str(compose)]
        for operation in (['config', '--quiet'], ['up', '-d', '--no-deps', '--scale', 'api=2', '--wait', '--wait-timeout', '120', 'api']):
            result = subprocess.run(command + operation, env=env, capture_output=True, timeout=180)
            if result.returncode:
                raise RuntimeError('Replica refresh failed; details suppressed to protect runtime settings')
        # Only update the management helper after the new replicas are healthy.
        helper = '#!/bin/sh\nexport ATL_API_IMAGE=' + shlex.quote(api['Image']) + \
                 '\nexport ATL_RUNTIME_ENV=' + shlex.quote(str(runtime)) + \
                 '\nexport ATL_SHARED_NETWORK=coolify\nexec docker compose -f ' + shlex.quote(str(compose)) + ' "$@"\n'
        atomic_private(RUNTIME / 'manage.sh', helper, 0o700)

    def activate(self):
        route.main(['--execute'], _locked=True)


def reconcile(host, maximum):
    if not ENABLED.is_file() or ENABLED.is_symlink():
        return {'action': 'disabled'}
    api, worker, replicas = host.snapshot()
    try:
        plan = decide(api, worker, replicas, maximum)
    except ValueError:
        # A changed pool/config must never silently produce extra DB clients.
        host.original_ready(api)
        host.fallback()
        ENABLED.unlink(missing_ok=True)
        raise
    active = owned_route()
    if plan['action'] == 'wait-api':
        return plan  # Do not route away from known-working copies to an unhealthy API.
    host.original_ready(api)
    if plan['action'] == 'wait-worker':
        if active:
            host.fallback()
        return plan
    if plan['action'] == 'aligned' and active:
        return plan
    try:
        host.fallback()
        if plan['action'] == 'refresh':
            host.refresh(api)
        # Detect another deployment or settings rotation during refresh.
        current_api, current_worker, current_replicas = host.snapshot()
        current = decide(current_api, current_worker, current_replicas, maximum)
        if current['action'] != 'aligned' or current.get('commit') != plan['commit']:
            raise RuntimeError('Release changed while refreshing; staying on original API')
        if not ENABLED.is_file():
            return {'action': 'disabled'}
        host.activate()
        return dict(current, action='updated')
    except BaseException:
        # Avoid endless refresh/retry loops; preserve current original API release.
        ENABLED.unlink(missing_ok=True)
        if owned_route():
            host.fallback()
        raise


def systemctl(*args):
    result = subprocess.run(['systemctl', *args], capture_output=True, timeout=30)
    if result.returncode:
        raise RuntimeError('Systemd operation failed (private output suppressed)')


def install(maximum):
    if not owned_route():
        raise ValueError('Installation requires the accepted active replica route')
    host = Host()
    api, worker, replicas = host.snapshot()
    plan = decide(api, worker, replicas, maximum)
    if plan['action'] != 'aligned':
        raise ValueError('Install only after original API, worker and replicas are aligned and healthy')
    host.original_ready(api)
    # Freeze reviewed controller code; later git checkouts cannot change the timer.
    files = ['scripts/replica-pilot.py', 'scripts/replica-route.py', 'scripts/replica-release.py',
             'deploy/scaling/compose.yml', 'deploy/scaling/haproxy.cfg']
    signature = hashlib.sha256(b''.join((ROOT / name).read_bytes() for name in files)).hexdigest()[:16]
    controllers = RUNTIME / 'controllers'
    if controllers.is_symlink():
        raise ValueError('Controller directory must not be a symlink')
    controllers.mkdir(mode=0o700, exist_ok=True)
    pinned = controllers / signature
    if not pinned.exists():
        pinned.mkdir(mode=0o700)
        for name in files:
            target = pinned / name
            target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            target.write_bytes((ROOT / name).read_bytes())
            target.chmod(0o600)
    elif pinned.is_symlink() or any((pinned / name).is_symlink() or (pinned / name).read_bytes() != (ROOT / name).read_bytes() for name in files):
        raise ValueError('Pinned controller differs from reviewed content')
    units = Path('/etc/systemd/system')
    service = units / (UNIT + '.service')
    timer = units / (UNIT + '.timer')
    marker = '# Managed by Atl xpres replica-release.py\n'
    for path in (service, timer):
        if path.is_symlink() or (path.exists() and not path.read_text().startswith(marker)):
            raise ValueError('Refusing to replace an unrelated systemd unit')
    command = '/usr/bin/python3 ' + str(pinned / 'scripts/replica-release.py') + ' --reconcile --max-connections ' + str(maximum)
    atomic_private(service, marker + '[Unit]\nDescription=Atl xpres replica release reconciliation\nAfter=docker.service\n\n[Service]\nType=oneshot\nExecStart=' + command + '\nTimeoutStartSec=600\nUMask=0077\n', 0o644)
    atomic_private(timer, marker + '[Unit]\nDescription=Check Atl xpres replica release consistency\n\n[Timer]\nOnActiveSec=15s\nOnUnitInactiveSec=15s\nAccuracySec=2s\nUnit=' + UNIT + '.service\n\n[Install]\nWantedBy=timers.target\n', 0o644)
    try:
        systemctl('daemon-reload')
        atomic_private(ENABLED, 'enabled\n')
        systemctl('enable', '--now', UNIT + '.timer')
    except BaseException:
        ENABLED.unlink(missing_ok=True)
        raise
    return dict(plan, action='installed', check_interval_seconds=15)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--max-connections', type=int, default=100)
    group = parser.add_mutually_exclusive_group()
    group.add_argument('--install', action='store_true')
    group.add_argument('--reconcile', action='store_true')
    group.add_argument('--pause', action='store_true')
    args = parser.parse_args()
    if not 1 <= args.max_connections <= 10000:
        raise ValueError('Provide the verified PostgreSQL connection limit')
    if not (args.install or args.reconcile or args.pause):
        api, worker, replicas = Host().snapshot()
        print(json.dumps(dict(decide(api, worker, replicas, args.max_connections), route_active=owned_route(),
                              automation_enabled=ENABLED.is_file()), sort_keys=True))
        print('Preview only: no files, timers, containers or routes changed.')
        return
    if not hasattr(os, 'geteuid') or os.geteuid() != 0:
        raise ValueError('Execution requires root on the main Linux server')
    try:
        with pilot.operation_lock(RUNTIME):
            if args.install:
                result = install(args.max_connections)
            elif args.pause:
                ENABLED.unlink(missing_ok=True)
                if owned_route():
                    route.rollback()
                result = {'action': 'paused', 'traffic': 'original API'}
            else:
                result = reconcile(Host(), args.max_connections)
    except pilot.OperationBusy:
        if not args.reconcile:
            raise
        result = {'action': 'busy', 'reason': 'Another route/release operation holds the lock'}
    print(json.dumps(result, sort_keys=True))


if __name__ == '__main__':
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, interrupted)
    try:
        main()
    except (ValueError, RuntimeError) as error:
        print('STOPPED:', str(error), file=sys.stderr)
        sys.exit(1)
    except (OSError, KeyError, json.JSONDecodeError, subprocess.TimeoutExpired, KeyboardInterrupt) as error:
        print('STOPPED:', type(error).__name__, '(private details suppressed)', file=sys.stderr)
        sys.exit(1)
