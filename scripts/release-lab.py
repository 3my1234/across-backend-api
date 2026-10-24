"""Coordinated isolated CI release; fixed synthetic targets, never production."""
import base64
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
PROJECT = 'atl-foundation-release'


def main():
    if os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('ATL_RELEASE_LAB') != 'synthetic-only':
        raise SystemExit('STOPPED: disposable CI release qualification only')
    evidence = []

    def run(args, env, timeout=150):
        result = subprocess.run(args, capture_output=True, text=True, env=env, timeout=timeout)
        if result.returncode:
            # All inputs are synthetic, but do not dump credential/token-like logs.
            raise RuntimeError('isolated release operation failed: '+args[0]+' '+args[1])
        return result.stdout.strip()

    with tempfile.TemporaryDirectory(prefix='atl-release-') as directory:
        fixture = Path(directory)
        fixture.chmod(0o755)
        env = dict(os.environ, LAB_TLS_DIR=str(fixture), LAB_REDIS_CA_BASE64='not-yet-generated', ATL_RELEASE_IMAGE='atl-release-new:local')
        run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
             '-subj', '/CN=ATL disposable release CA', '-addext', 'subjectAltName=DNS:postgres,DNS:redis',
             '-keyout', str(fixture/'server.key'), '-out', str(fixture/'ca.pem')], env)
        # Distinct disposable keys/files so each image's actual UID can read
        # only its own private key. No production certificate/key enters CI.
        (fixture/'redis.key').write_bytes((fixture/'server.key').read_bytes())
        pg_uid = run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint', 'id', 'postgres:16-alpine', '-u', 'postgres'], env)
        redis_uid = run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint', 'id', 'redis:7.2.16', '-u', 'redis'], env)
        if not pg_uid.isdigit() or not redis_uid.isdigit():
            raise RuntimeError('fixture service UIDs unavailable')
        run(['sudo', 'chown', pg_uid+':'+pg_uid, str(fixture/'server.key')], env)
        run(['sudo', 'chmod', '600', str(fixture/'server.key')], env)
        run(['sudo', 'chown', redis_uid+':'+redis_uid, str(fixture/'redis.key')], env)
        run(['sudo', 'chmod', '600', str(fixture/'redis.key')], env)
        env['LAB_REDIS_CA_BASE64'] = base64.b64encode((fixture/'ca.pem').read_bytes()).decode()
        (fixture/'redis.conf').write_text('port 0\ntls-port 6380\ntls-cert-file /fixture/ca.pem\ntls-key-file /fixture/redis.key\ntls-ca-cert-file /fixture/ca.pem\ntls-auth-clients optional\nrequirepass release-lab-only\nappendonly yes\nappendfsync everysec\ndir /data\n')
        (fixture/'haproxy.cfg').write_text('global\n  maxconn 128\ndefaults\n  mode http\n  timeout connect 2s\n  timeout client 30s\n  timeout server 30s\n  retries 0\nfrontend api\n  bind :8080\n  option forwardfor\n  default_backend copies\nbackend copies\n  balance roundrobin\n  http-response set-header X-Lab-Backend %[srv_name]\n  server one api1:8080 check\n  server two api2:8080 check\n')
        compose = ['docker', 'compose', '-p', PROJECT, '-f', str(ROOT/'deploy/release-lab/compose.yml')]

        def dc(*args, timeout=150):
            return run(compose+list(args), env, timeout)

        def ready(service):
            return dc('exec', '-T', service, 'curl', '-sS', '--max-time', '5', '-o', '/dev/null', '-w', '%{http_code}', 'http://localhost:8080/api/v1/ready')

        def wait_ready():
            for attempt in range(30):
                try:
                    if all(ready(name) == '200' for name in ('original', 'api1', 'api2', 'worker')):
                        return
                except RuntimeError:
                    pass
                time.sleep(1)
            raise RuntimeError('isolated release did not regain readiness')

        def counts():
            sql = "SELECT json_build_object('users',(SELECT count(*) FROM users),'providers',(SELECT count(*) FROM provider_organizations),'messages',(SELECT count(*) FROM provider_conversation_messages),'town_hall_messages',(SELECT count(*) FROM chat_messages),'subscription_payments',(SELECT count(*) FROM provider_subscription_payments),'migrations',(SELECT count(*) FROM schema_migrations))"
            return json.loads(dc('exec', '-T', 'postgres', 'psql', '-X', '-At', '-U', 'lab', '-d', 'capacity_lab', '-c', sql))

        try:
            dc('up', '-d', '--wait', '--wait-timeout', '90', 'postgres', 'redis')
            dc('run', '--rm', '--no-deps', 'seed')
            dc('up', '-d', '--wait', '--wait-timeout', '120', 'original', 'api1', 'api2', 'worker')
            dc('up', '-d', 'gateway')
            dc('run', '--rm', '--no-deps', 'runner', '/app/lab', 'prepare-sessions')
            dc('run', '--rm', '--no-deps', 'runner', '/app/lab', 'release-check')
            dc('run', '--rm', '--no-deps', 'runner', '/app/lab', 'lifecycle')
            before = counts()
            dc('exec', '-T', 'redis', 'redis-cli', '--tls', '--cacert', '/fixture/ca.pem', '-h', 'redis', '-p', '6380', 'SET', 'release:sentinel', 'retained')
            dc('stop', '-t', '5', 'redis')
            if ready('original') != '503' or ready('worker') != '503':
                raise RuntimeError('dependency outage was not reflected in readiness')
            dc('up', '-d', '--no-deps', '--wait', '--wait-timeout', '60', 'redis')
            wait_ready()
            value = dc('exec', '-T', 'redis', 'redis-cli', '--tls', '--cacert', '/fixture/ca.pem', '-h', 'redis', '-p', '6380', 'GET', 'release:sentinel')
            if value != 'retained' or counts() != before:
                raise RuntimeError('recovery changed business counts or lost AOF sentinel')
            evidence.append({'redis_interruption_recovery': 'pass', 'business_counts': before})
            # Actual retained application code, built separately from ec60b2a.
            env['ATL_RELEASE_IMAGE'] = 'atl-release-old:local'
            dc('up', '-d', '--no-deps', '--force-recreate', '--wait', '--wait-timeout', '120', 'original', 'api1', 'api2', 'worker')
            wait_ready()
            if counts() != before:
                raise RuntimeError('old-code rollback changed retained business data')
            dc('run', '--rm', '--no-deps', 'runner', '/app/lab', 'lifecycle')
            dc('run', '--rm', '--no-deps', 'runner', '/app/lab', 'release-check')
            after_rollback = counts()
            evidence.append({'retained_code_rollback': 'pass', 'tls_still_verified': True})
            env['ATL_RELEASE_IMAGE'] = 'atl-release-new:local'
            dc('up', '-d', '--no-deps', '--force-recreate', '--wait', '--wait-timeout', '120', 'original', 'api1', 'api2', 'worker')
            wait_ready()
            if counts() != after_rollback:
                raise RuntimeError('forward release lost writes made through retained code')
            evidence.append({'forward_release_after_rollback': 'pass'})
        finally:
            dc('down', '--volumes', '--remove-orphans')
        print(json.dumps({'coordinated_release_passed': True, 'external_delivery': 'blocked by internal network; no production credentials', 'checks': evidence, 'scope': 'synthetic process/transport/rollback; not production capacity, tunnel or real device delivery', 'owned_cleanup_complete': True}))


if __name__ == '__main__':
    main()
