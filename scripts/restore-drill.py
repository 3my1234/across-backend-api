#!/usr/bin/env python3
"""Restore a local custom-format archive into a newly created, isolated container.

No production URI/host option exists. Preview is default. Images must be local.
Never prints archive contents, SQL errors, credentials or personal records.
"""
import argparse
import csv
import hashlib
import io
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

PHASE = 'preflight'

INVENTORY = frozenset(('providers', 'members', 'documents', 'listings', 'products', 'subscriptions',
                       'subscription_payments', 'payments', 'payment_webhooks', 'conversations',
                       'ordinary_messages', 'rooms', 'participants', 'shared_messages',
                       'town_hall_requests', 'town_hall_offers', 'town_hall_applications',
                       'renewal_preferences', 'renewal_cancellations'))
FINDINGS = frozenset(('provider_missing_owner', 'provider_missing_owner_membership',
    'listing_missing_provider', 'subscription_missing_provider_or_plan', 'subscription_period_invalid',
    'subscription_payment_invalid_reference_or_amount', 'payment_purpose_link_invalid',
    'payment_idempotency_duplicate_groups', 'payment_transaction_duplicate_groups', 'webhook_duplicate_groups',
    'conversation_thread_pair_mismatch', 'ordinary_message_missing_conversation_or_sender',
    'ordinary_message_retry_duplicate_groups', 'shared_message_missing_room_or_sender',
    'shared_message_retry_duplicate_groups', 'town_hall_wrong_room_kind',
    'town_hall_over_ten_active_provider_seats', 'unvalidated_public_constraints', 'invalid_public_indexes',
    'subscription_ledger_amount_mismatch', 'subscription_payment_identity_mismatch',
    'town_hall_offer_missing_request_or_provider', 'chat_reply_crosses_room'))


def parse_aggregates(raw):
    aggregates = {}
    for row in csv.reader(io.StringIO(raw)):
        if len(row) == 2 and row[0] in INVENTORY | FINDINGS and re.fullmatch(r'\d+', row[1]):
            if row[0] in aggregates:
                raise RuntimeError('duplicate aggregate label')
            aggregates[row[0]] = int(row[1])
        elif len(row) == 4:
            continue  # Grouped summaries are exercised but never emitted.
        else:
            raise RuntimeError('unexpected aggregate output')
    if set(aggregates) != INVENTORY | FINDINGS:
        raise RuntimeError('incomplete aggregate validation')
    return aggregates


def docker(*args, stdin=None):
    # subprocess requires a real file descriptor for stdin; in-memory SQL must
    # use input=bytes. File-backed archives/executables remain streamed.
    source = {'input': stdin.getvalue()} if isinstance(stdin, io.BytesIO) else {'stdin': stdin}
    result = subprocess.run(['docker', *args], **source, stdout=subprocess.PIPE,
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
            target['HostConfig'].get('Binds') or
            any(mount.get('Type') != 'tmpfs' for mount in target.get('Mounts', [])) or
            target['HostConfig'].get('Privileged') or
            target['Config'].get('User') != 'postgres'):
        raise RuntimeError('STOPPED: isolation/ownership check failed')


def extended_checks(name, nonce, executable):
    """Run bundled aggregate SQL and sampled real handlers before cleanup."""
    global PHASE
    PHASE = 'extended-role'
    verify_target(name, nonce)
    # This role exists only in the freshly restored, network-none test target.
    # No password or production role is copied into the handler harness.
    role_sql = b'''CREATE ROLE recovery_reader LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE;
GRANT CONNECT ON DATABASE recovery_drill TO recovery_reader;
GRANT USAGE ON SCHEMA public TO recovery_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO recovery_reader;
ALTER ROLE recovery_reader SET default_transaction_read_only=on;
ALTER ROLE recovery_reader SET statement_timeout='5s';
ALTER ROLE recovery_reader SET lock_timeout='1s';
'''
    docker('exec', '-i', name, 'psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1',
           '-U', 'postgres', '-d', 'recovery_drill', stdin=io.BytesIO(role_sql))
    PHASE = 'aggregate-validation'
    sql = Path(__file__).with_name('recovery-validation.sql').read_bytes()
    raw = docker('exec', '-i', name, 'psql', '-X', '-q', '-t', '--csv', '-v', 'ON_ERROR_STOP=1',
                 '-U', 'recovery_reader', '-d', 'recovery_drill', stdin=io.BytesIO(sql))
    # Only fixed label/numeric output from the bundled SQL is accepted. Never
    # expose a database error, raw row or a currency/status string from data.
    aggregates = parse_aggregates(raw)
    PHASE = 'application-check-upload'
    with executable.open('rb') as binary:
        docker('exec', '-i', name, 'sh', '-c',
               'umask 077; cat > /tmp/atl-recovery-read-check && chmod 500 /tmp/atl-recovery-read-check', stdin=binary)
    PHASE = 'application-reads'
    application = json.loads(docker('exec', name, '/tmp/atl-recovery-read-check'))
    if application.get('application_reads_passed') is not True:
        raise RuntimeError('application reads failed')
    findings = {key: aggregates[key] for key in FINDINGS}
    return {'aggregate_counts': {key: aggregates[key] for key in INVENTORY}, 'findings': findings,
            'findings_require_review': any(findings.values()), 'application_reads': application}


def main():
    global PHASE
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive', required=True, type=Path)
    parser.add_argument('--postgres-major', required=True, choices=['15', '16', '17', '18'])
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--read-check', type=Path,
                        help='reviewed static Linux recovery-read-check executable; enables extended SQL/handler checks')
    args = parser.parse_args()
    path, checksum = inspect_archive(args.archive)
    executable = args.read_check.resolve(strict=True) if args.read_check else None
    binary_checksum = None
    if executable:
        with executable.open('rb') as stream:
            if stream.read(4) != b'\x7fELF':
                raise ValueError('a reviewed Linux ELF read-check executable is required')
            stream.seek(0)
            binary_checksum = hashlib.file_digest(stream, 'sha256').hexdigest()
    print(json.dumps({'archive_sha256': checksum, 'bytes': path.stat().st_size,
                      'target': 'new isolated recovery_drill database; no production connection',
                      'execute': args.execute, 'extended_checks': executable is not None,
                      'read_check_sha256': binary_checksum}))
    if not args.execute:
        return
    nonce = uuid.uuid4().hex
    name = 'atl-restore-drill-' + nonce
    image = 'postgres:' + args.postgres_major
    docker('image', 'inspect', image)  # No implicit downloads during a drill.
    created = False
    began = time.monotonic()
    try:
        PHASE = 'container-create'
        docker('create', '--pull=never', '--name', name, '--label', 'atl.restore-drill=' + nonce,
               '--network', 'none', '--user', 'postgres', '--cap-drop', 'ALL',
               '--security-opt', 'no-new-privileges', '--memory', '1g', '--cpus', '1',
               '--pids-limit', '128', '--tmpfs', '/var/lib/postgresql:rw,mode=1777,size=536870912',
               '--tmpfs', '/var/lib/postgresql/data:rw,mode=1777,size=1048576',
               '-e', 'PGDATA=/var/lib/postgresql/drill', '-e', 'POSTGRES_DB=recovery_drill',
               '-e', 'POSTGRES_HOST_AUTH_METHOD=trust', image)
        created = True
        PHASE = 'isolation-verification'
        verify_target(name, nonce)
        PHASE = 'database-start'
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
            PHASE = 'archive-restore'
            docker('exec', '-i', name, 'pg_restore', '-U', 'postgres', '-d', 'recovery_drill',
                   '--no-owner', '--no-privileges', '--exit-on-error', '--single-transaction', stdin=archive)
        # Only aggregate counts, never row contents. Names below are fixed source code.
        checks = {}
        PHASE = 'structural-validation'
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
        extended = extended_checks(name, nonce, executable) if executable else None
        report = {'isolated_restore_passed': True, 'counts': checks,
                          'migration_inventory_matched': True, 'migration_count': len(applied),
                          'elapsed_seconds': round(time.monotonic() - began, 2),
                          'archive_sha256': checksum,
                          'extended_validation': extended,
                          'scope': 'structural restore; extended mode samples read handlers and aggregate invariants, not live integrations or complete recovery objectives'}
    finally:
        if created:
            # Preserve the failing stage if cleanup itself succeeds.
            failed_phase = PHASE
            PHASE = 'cleanup'
            verify_target(name, nonce)
            docker('rm', '-f', '-v', name)
            PHASE = failed_phase
    report['owned_container_removed'] = True
    print(json.dumps(report))


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired):
        raise SystemExit('STOPPED: restore drill did not complete at stage ' + PHASE + '; no production target was used. Output withheld to protect archive data.')
