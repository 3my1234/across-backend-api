"""Start only the loopback replica pilot; never alter production routing.

Reads runtime configuration internally from the existing API. Never print it.
Default is read-only preflight. --execute starts two bounded API copies and
HAProxy on 127.0.0.1:18080. No load test, migrations, DNS or cloud operations.
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

API_RESOURCE = 'x139kasktvpelpef1t6xgid3'
WORKER_RESOURCE = 'sx426mx9d9ugbeouvhk4dunj'
PROJECT = 'atlxpres-scale-pilot'


def docker(*args):
    result = subprocess.run(['docker', *args], capture_output=True, text=True, timeout=120)
    if result.returncode:
        # Docker/Compose errors can echo resolved runtime values. Keep them private.
        raise RuntimeError('Docker operation failed: ' + args[0] + ' (details suppressed to protect runtime settings)')
    return result.stdout


def environment(container):
    result = {}
    for entry in container['Config'].get('Env') or []:
        key, separator, value = entry.partition('=')
        if not separator or not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', key):
            raise ValueError('Invalid runtime environment entry')
        if '\n' in value or '\r' in value or '\x00' in value:
            raise ValueError('Multiline runtime values require a separately reviewed private configuration')
        result[key] = value
    return result


def pool_limit(env, name, default):
    value = env.get(name, '')
    if not value:
        return default
    if not value.isdecimal() or not 2 <= int(value) <= 1000:
        raise ValueError('Unsupported connection setting: ' + name)
    return int(value)


def connection_budget(containers, maximum):
    # Count all inherited read pools conservatively even if on another server.
    existing = sum(pool_limit(env, 'DB_MAX_CONNS', 20) +
                   (pool_limit(env, 'DB_READ_MAX_CONNS', 20) if env.get('DATABASE_READ_URL') else 0)
                   for env in map(environment, containers))
    # 20 reserved includes PG's 3 superuser slots, 10 additional clients,
    # two pilot replicas plus one rolling-update surge, 8 connections each.
    required = existing + 20 + 10 + 3 * 8
    if required > maximum:
        raise ValueError('Connection budget does not fit: required=%d maximum=%d; no containers started' % (required, maximum))
    return existing, required


def private_env(path, env):
    # O_EXCL prevents overwriting files/symlinks and avoids retaining stale secrets.
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'w', encoding='utf-8', newline='\n') as stream:
        for key, value in sorted(env.items()):
            if key not in {'HOSTNAME', 'PATH', 'HOME'}:
                stream.write(key + '=' + value + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--expected-commit', required=True)
    parser.add_argument('--max-connections', required=True, type=int)
    parser.add_argument('--execute', action='store_true')
    args = parser.parse_args()
    if not re.fullmatch(r'[a-f0-9]{40}', args.expected_commit) or not 1 <= args.max_connections <= 10000:
        raise ValueError('Use a full reviewed API commit and the verified PostgreSQL max_connections')
    if args.execute and (not hasattr(os, 'geteuid') or os.geteuid() != 0):
        raise ValueError('Run execution on the main Linux server as root')
    root = Path(__file__).resolve().parents[1]
    compose = root / 'deploy/scaling/compose.yml'
    if not compose.is_file() or not (compose.parent / 'haproxy.cfg').is_file():
        raise ValueError('Run from the complete reviewed repository checkout')
    inventory = [json.loads(line) for line in docker('ps', '--format', '{{json .}}').splitlines() if line]
    if not inventory:
        raise ValueError('No running containers found')
    ids = []
    for resource in (API_RESOURCE, WORKER_RESOURCE):
        matches = [c for c in inventory if c['Names'].startswith(resource + '-')]
        if len(matches) != 1:
            raise ValueError('Expected exactly one running ' + resource + '; wait for any rolling deployment to finish')
        ids.append(matches[0]['ID'])
    # Inspect only the application API/worker, never Coolify's own secrets.
    selected = json.loads(docker('inspect', *ids))
    api, worker = selected
    if not api['Config']['Image'].endswith(':' + args.expected_commit):
        raise ValueError('Live API image does not match the reviewed commit; no pilot started')
    for container in selected:
        if container['State'].get('Health', {}).get('Status') != 'healthy':
            raise ValueError('Existing API/worker must be healthy before starting pilot')
    env = environment(api)
    if env.get('RUN_INLINE_WORKERS', '').lower() != 'false':
        raise ValueError('Existing API inline workers must explicitly be false')
    if not env.get('REDIS_URL') and not env.get('REDIS_ADDR'):
        raise ValueError('Shared Redis configuration is required')
    if 'coolify' not in api['NetworkSettings']['Networks']:
        raise ValueError('Existing API is not on the expected coolify network')
    if docker('ps', '-q', '--filter', 'label=com.docker.compose.project=' + PROJECT).strip():
        raise ValueError('Pilot already running: inspect it instead of creating another copy')
    existing, required = connection_budget(selected, args.max_connections)
    ready = json.loads(docker('exec', api['Id'], 'curl', '-fsS', '--max-time', '5', 'http://127.0.0.1:8080/api/v1/ready'))
    if ready.get('ok') is not True:
        raise ValueError('Live API dependency readiness did not pass')
    docker('compose', 'version')
    print('Verified API commit:', args.expected_commit)
    print('Connection budget: existing API/worker maximum=%d; total including pilot surge, other clients and reserve=%d/%d' % (existing, required, args.max_connections))
    print('Pilot: two APIs, pool8 each, no read pool, no workers/migrations; gateway 127.0.0.1:18080 only.')
    if not args.execute:
        print('Preflight only; no files written or containers started.')
        return
    # Create one new private directory; never reuse or overwrite an earlier runtime.
    runtime = Path('/opt/atlxpres-scale-pilot-runtime')
    runtime.mkdir(mode=0o700, exist_ok=False)
    config = runtime / 'runtime.env'
    private_env(config, env)
    process_env = dict(os.environ, ATL_API_IMAGE=api['Image'], ATL_RUNTIME_ENV=str(config), ATL_SHARED_NETWORK='coolify')
    command = ['docker', 'compose', '-f', str(compose)]
    # Save only a private helper, with no secrets embedded, for status/down commands.
    helper = runtime / 'manage.sh'
    import shlex
    helper.write_text('#!/bin/sh\nexport ATL_API_IMAGE=' + shlex.quote(api['Image']) +
                      '\nexport ATL_RUNTIME_ENV=' + shlex.quote(str(config)) +
                      '\nexport ATL_SHARED_NETWORK=coolify\nexec docker compose -f ' + shlex.quote(str(compose)) + ' "$@"\n', encoding='utf-8')
    helper.chmod(0o700)
    for operation in (['config', '--quiet'], ['up', '-d', '--scale', 'api=2', '--wait', '--wait-timeout', '120']):
        result = subprocess.run(command + operation, env=process_env, capture_output=True, text=True, timeout=180)
        if result.returncode:
            raise RuntimeError('Pilot startup did not complete. Production routing is unchanged. Use /opt/atlxpres-scale-pilot-runtime/manage.sh ps or down. Do not print runtime.env or resolved Compose configuration.')
    print('Pilot started. Production API, DNS and Traefik routing unchanged.')
    print('Status: /opt/atlxpres-scale-pilot-runtime/manage.sh ps')
    print('Remove pilot containers only: /opt/atlxpres-scale-pilot-runtime/manage.sh down')
    print('Next: small loopback readiness probes; NO load testing against this production database.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, RuntimeError, OSError, subprocess.TimeoutExpired, KeyError, json.JSONDecodeError) as error:
        # Never emit environment/config contents or subprocess output.
        if isinstance(error, (ValueError, RuntimeError)):
            print('STOPPED:', str(error), file=sys.stderr)
        else:
            print('STOPPED:', type(error).__name__, '(details suppressed)', file=sys.stderr)
        sys.exit(1)
