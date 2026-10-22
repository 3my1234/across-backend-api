#!/usr/bin/env python3
"""Restore a local custom-format archive into a newly created, isolated container.

No production URI/host option exists. Preview is default. Images must be local.
Never prints archive contents, SQL errors, credentials or personal records.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import time
import uuid

REQUIRED_TABLES = ('users', 'provider_organizations', 'provider_members',
                   'provider_verification_documents', 'provider_listings', 'products',
                   'provider_subscription_plans', 'provider_subscriptions',
                   'provider_subscription_payments', 'payments', 'payment_webhook_events',
                   'provider_conversations', 'provider_conversation_messages',
                   'chat_rooms', 'chat_participants', 'chat_messages',
                   'town_hall_requests', 'town_hall_offers', 'schema_migrations')


def docker(*args, stdin=None):
    result = subprocess.run(['docker', *args], stdin=stdin, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, timeout=180)
    if result.returncode:
        raise RuntimeError('isolated Docker operation failed (output withheld to protect archive data)')
    return result.stdout.decode('utf-8').strip()


def inspect_archive(path):
    path = Path(path).resolve(strict=True)
    digest = hashlib.sha256()
    with path.open('rb') as source:
        if source.read(5) != b'PGDMP':
            raise ValueError('a PostgreSQL custom-format PGDMP archive is required')
        source.seek(0)
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            digest.update(chunk)
    return path, digest.hexdigest()


def verify_target(name, nonce):
    target = json.loads(docker('inspect', name))[0]
    if (target['Config'].get('Labels', {}).get('atl.restore-drill') != nonce or
            target['HostConfig']['NetworkMode'] != 'none' or
            target['HostConfig'].get('PortBindings') or
            target['Config'].get('User') != 'postgres'):
        raise RuntimeError('STOPPED: isolation/ownership check failed')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive', required=True, type=Path)
    parser.add_argument('--postgres-major', required=True, choices=['15', '16', '17', '18'])
    parser.add_argument('--execute', action='store_true')
    args = parser.parse_args()
    path, checksum = inspect_archive(args.archive)
    print(json.dumps({'archive_sha256': checksum, 'bytes': path.stat().st_size,
                      'target': 'new isolated recovery_drill database; no production connection',
                      'execute': args.execute}))
    if not args.execute:
        return
    nonce = uuid.uuid4().hex
    name = 'atl-restore-drill-' + nonce
    image = 'postgres:' + args.postgres_major
    docker('image', 'inspect', image)  # No implicit downloads during a drill.
    created = False
    began = time.monotonic()
    try:
        docker('create', '--pull=never', '--name', name, '--label', 'atl.restore-drill=' + nonce,
               '--network', 'none', '--user', 'postgres', '--cap-drop', 'ALL',
               '--security-opt', 'no-new-privileges', '--memory', '1g', '--cpus', '1',
               '--pids-limit', '128', '--tmpfs', '/var/lib/postgresql:rw,mode=1777,size=536870912',
               '-e', 'PGDATA=/var/lib/postgresql/drill', '-e', 'POSTGRES_DB=recovery_drill',
               '-e', 'POSTGRES_HOST_AUTH_METHOD=trust', image)
        created = True
        verify_target(name, nonce)
        docker('start', name)
        for _ in range(60):
            try:
                if 'PostgreSQL init process complete; ready for start up.' not in docker('logs', name):
                    time.sleep(0.5)
                    continue
                docker('exec', name, 'pg_isready', '-U', 'postgres', '-d', 'recovery_drill')
                break
            except RuntimeError:
                time.sleep(0.5)
        else:
            raise RuntimeError('isolated database did not start')
        # The archive is streamed; no production file or directory is mounted.
        with path.open('rb') as archive:
            docker('exec', '-i', name, 'pg_restore', '-U', 'postgres', '-d', 'recovery_drill',
                   '--no-owner', '--no-privileges', '--exit-on-error', '--single-transaction', stdin=archive)
        # Only aggregate counts, never row contents. Names below are fixed source code.
        checks = {}
        for table in REQUIRED_TABLES:
            raw = docker('exec', name, 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres',
                         '-d', 'recovery_drill', '-Atc', 'SELECT count(*) FROM public.' + table)
            if not re.fullmatch(r'\d+', raw):
                raise RuntimeError('required data check failed')
            checks[table] = int(raw)
        migrations = Path(__file__).resolve().parents[1] / 'migrations'
        expected = {entry.name for entry in migrations.glob('*.sql')}
        if not expected:
            raise RuntimeError('reviewed migration source is required for completeness checks')
        applied = set(docker('exec', name, 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres',
                             '-d', 'recovery_drill', '-Atc', 'SELECT name FROM public.schema_migrations').splitlines())
        if expected != applied:
            raise RuntimeError('restored migration inventory differs from reviewed source; assess archive/release compatibility')
        constraints = docker('exec', name, 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres',
                             '-d', 'recovery_drill', '-Atc',
                             "SELECT count(*) FROM pg_constraint WHERE connamespace='public'::regnamespace AND NOT convalidated")
        if constraints != '0':
            raise RuntimeError('unvalidated constraints require review before recovery can pass')
        print(json.dumps({'isolated_restore_passed': True, 'counts': checks,
                          'migration_inventory_matched': True, 'migration_count': len(applied),
                          'elapsed_seconds': round(time.monotonic() - began, 2),
                          'archive_sha256': checksum,
                          'scope': 'archive restored and structural checks passed; application/payment checks and recovery objectives still require review'}))
    finally:
        if created:
            verify_target(name, nonce)
            docker('rm', '-f', '-v', name)


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired):
        raise SystemExit('STOPPED: restore drill did not complete; no production target was used. Check local image, archive format/version, size limits and isolated environment.')
