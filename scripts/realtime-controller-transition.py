"""Stage the reviewed gateway controller with a guarded fallback. Preview by default.

No API/worker deploy, migration, load test, or application write is performed.
Run only from the complete reviewed controller checkout on the main Linux host.
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
import uuid

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('release', ROOT / 'scripts/replica-release.py')
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)
runtime = release.RUNTIME
service = Path('/etc/systemd/system/atlxpres-replica-release.service')
timer = Path('/etc/systemd/system/atlxpres-replica-release.timer')
helper = runtime / 'manage.sh'
expected = '546a1d2a9680d428d29398a25c12f539f139994ba04a7582f38503eedb38d32f'
files = ['scripts/replica-pilot.py', 'scripts/replica-route.py', 'scripts/replica-release.py',
         'deploy/scaling/compose.yml', 'deploy/scaling/haproxy.cfg']

def require_file(path):
    if path.is_symlink() or not path.is_file() or path.stat().st_uid != 0:
        raise ValueError('Expected root-owned regular file: ' + path.name)
    return path.read_bytes()

def checked_helper(contents, api_image):
    lines = contents.decode('utf-8').splitlines()
    if len(lines) != 5 or lines[0] != '#!/bin/sh': raise ValueError('Unexpected management helper layout')
    expected_names = ['ATL_API_IMAGE', 'ATL_RUNTIME_ENV', 'ATL_SHARED_NETWORK']
    values = {}
    for line, name in zip(lines[1:4], expected_names):
        parts = shlex.split(line)
        if len(parts) != 2 or parts[0] != 'export' or not parts[1].startswith(name + '='):
            raise ValueError('Unexpected management helper export')
        values[name] = parts[1].split('=', 1)[1]
    if values['ATL_API_IMAGE'] != api_image or values['ATL_SHARED_NETWORK'] != 'coolify':
        raise ValueError('Management helper image or network differs from live API')
    envpath = Path(values['ATL_RUNTIME_ENV'])
    if not envpath.is_absolute() or not envpath.is_relative_to(runtime / 'releases'):
        raise ValueError('Unexpected private runtime file')
    require_file(envpath)
    command = shlex.split(lines[4])
    if len(command) != 6 or command[:4] != ['exec', 'docker', 'compose', '-f'] or command[5] != '$@':
        raise ValueError('Unexpected management helper command')
    compose = Path(command[4])
    if not compose.is_absolute() or not compose.is_relative_to(runtime / 'controllers'):
        raise ValueError('Unexpected controller compose path')
    require_file(compose)
    return lines

def run(execute=False):
    if os.geteuid() != 0: raise ValueError('Root is required')
    candidate = require_file(ROOT / 'deploy/scaling/haproxy.cfg')
    if hashlib.sha256(candidate.replace(b'\r\n', b'\n').rstrip(b'\r\n')).hexdigest() != expected:
        raise ValueError('Gateway candidate differs from the validated configuration')
    for name in files: require_file(ROOT / name)
    old_service, old_timer, old_helper = [require_file(p) for p in (service, timer, helper)]
    if not old_service.startswith(b'# Managed by Atl xpres replica-release.py\n') or not old_timer.startswith(b'# Managed by Atl xpres replica-release.py\n'):
        raise ValueError('Unexpected systemd units')
    if not release.ENABLED.is_file() or release.ENABLED.is_symlink(): raise ValueError('Controller automation is not enabled')
    if not release.owned_route(): raise ValueError('Expected an owned, unchanged replica route')
    if subprocess.run(['systemctl', 'is-active', '--quiet', release.UNIT + '.timer'], timeout=10).returncode:
        raise ValueError('Controller timer is not active')
    host = release.Host()
    api, worker, replicas = host.snapshot()
    plan = release.decide(api, worker, replicas, 100)
    if plan['action'] != 'aligned': raise ValueError('API, worker and replicas are not aligned and healthy')
    host.original_ready(api)
    lines = checked_helper(old_helper, api['Image'])
    old_compose = Path(shlex.split(lines[4])[4])
    live_gateway = json.loads(release.pilot.docker('inspect', release.route.GATEWAY))[0]
    old_config = old_compose.parent / 'haproxy.cfg'
    old_gateway = require_file(old_config)
    if not any(m.get('Source') == str(old_config) and m.get('Destination') == '/usr/local/etc/haproxy/haproxy.cfg' for m in live_gateway.get('Mounts', [])):
        raise ValueError('Live gateway mount differs from the rollback helper')
    unit_info = subprocess.run(['systemctl', 'show', release.UNIT + '.service', '--property=ExecStart', '--value', '--no-pager'], capture_output=True, text=True, timeout=10)
    if unit_info.returncode or str(old_compose.parents[2] / 'scripts/replica-release.py') not in unit_info.stdout:
        raise ValueError('Controller service and rollback helper are not pinned to the same source')
    check = subprocess.run(['docker', 'exec', '-i', release.route.GATEWAY, 'haproxy', '-c', '-f', '/dev/stdin'], input=candidate, capture_output=True, timeout=30)
    if check.returncode: raise ValueError('Gateway candidate did not pass HAProxy validation')
    signature = hashlib.sha256(b''.join((ROOT / name).read_bytes() for name in files)).hexdigest()[:16]
    pinned = runtime / 'controllers' / signature
    print(json.dumps({'action': 'ready' if execute else 'preview', 'api_commit': plan['commit'], 'candidate_controller': signature, 'gateway_syntax': 'passed', 'route_owned': True}), flush=True)
    if not execute: return
    # Both the timer and this transition use the same lock. Backup files are
    # root-private; helper contents may identify sensitive runtime locations.
    backup = runtime / ('realtime-controller-backup-' + uuid.uuid4().hex)
    backup.mkdir(mode=0o700)
    old_route = require_file(release.route.ROUTE)
    old_route_state = require_file(release.route.STATE)
    for name, data in [('service', old_service), ('timer', old_timer), ('helper', old_helper),
                       ('haproxy.cfg', old_gateway), ('route.yaml', old_route), ('route-state.json', old_route_state)]:
        target = backup / name
        with os.fdopen(os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'wb') as out:
            out.write(data); out.flush(); os.fsync(out.fileno())
    changed = False
    try:
        changed = True
        release.install(100)
        if (pinned / 'deploy/scaling/haproxy.cfg').read_bytes() != candidate: raise RuntimeError('Pinned gateway differs from reviewed candidate')
        host.fallback()
        new_helper = '\n'.join(lines[:4] + ['exec docker compose -f ' + shlex.quote(str(pinned / 'deploy/scaling/compose.yml')) + ' "$@"']) + '\n'
        release.atomic_private(helper, new_helper, 0o700)
        host.activate()
        info = json.loads(release.pilot.docker('inspect', release.route.GATEWAY))[0]
        mounts = info.get('Mounts', [])
        if not any(m.get('Source') == str(pinned / 'deploy/scaling/haproxy.cfg') and m.get('Destination') == '/usr/local/etc/haproxy/haproxy.cfg' for m in mounts):
            raise RuntimeError('Running gateway does not mount the reviewed candidate')
        print(json.dumps({'action': 'transitioned', 'controller': signature, 'backup': str(backup), 'live_probe': 'passed'}), flush=True)
    except BaseException:
        if changed:
            try:
                if release.owned_route(): host.fallback()
                release.ENABLED.unlink(missing_ok=True)
                release.atomic_private(helper, old_helper.decode('utf-8'), 0o700)
                release.atomic_private(service, old_service.decode('utf-8'), 0o644)
                release.atomic_private(timer, old_timer.decode('utf-8'), 0o644)
                release.systemctl('daemon-reload')
                # Restore old gateway and route while automatic reconciliation is disabled.
                host.activate()
                release.atomic_private(release.ENABLED, 'enabled\n')
                release.systemctl('enable', '--now', release.UNIT + '.timer')
                print(json.dumps({'action': 'rolled-back', 'backup': str(backup), 'live_probe': 'passed'}), flush=True)
            except BaseException:
                release.ENABLED.unlink(missing_ok=True)
                print(json.dumps({'action': 'rollback-incomplete', 'backup': str(backup), 'automatic_updates': 'disabled'}), flush=True)
        raise

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--execute', action='store_true')
    args = parser.parse_args()
    with release.pilot.operation_lock(runtime): run(args.execute)

if __name__ == '__main__':
    def interrupted(_signal, _frame): raise InterruptedError('Controller transition interrupted')
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGHUP, interrupted)
    try: main()
    except (ValueError, RuntimeError, OSError, subprocess.TimeoutExpired) as exc:
        print('STOP: ' + str(exc), file=sys.stderr)
        sys.exit(1)
