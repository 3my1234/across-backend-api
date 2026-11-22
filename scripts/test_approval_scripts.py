"""Run approval SQL checks only on a disposable loopback PostgreSQL server."""
import concurrent.futures
import os
import pathlib
import subprocess
import uuid

PSQL = os.environ["LOCAL_TEST_PSQL"]
ROOT = pathlib.Path(__file__).resolve().parent
schema = "approval_test_" + uuid.uuid4().hex
base = [PSQL, "-X", "-h", "127.0.0.1", "-p", "55478", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1"]

def run(*args, env=None, ok=True):
    result = subprocess.run(base + list(args), env=env, text=True, capture_output=True)
    if (result.returncode == 0) != ok:
        raise AssertionError(result.stdout + result.stderr)
    return result.stdout

run("-c", f'CREATE SCHEMA "{schema}"')
env = dict(os.environ, PGOPTIONS=f"-c search_path={schema}")
try:
    run("-c", "CREATE TABLE launch_waitlist(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),email text UNIQUE NOT NULL,approved boolean NOT NULL DEFAULT false,created_at timestamptz NOT NULL DEFAULT now())", env=env)
    run("-c", "CREATE TABLE signup_approved_emails(email text PRIMARY KEY,created_at timestamptz NOT NULL DEFAULT now())", env=env)
    run("-c", "INSERT INTO launch_waitlist(email,created_at) SELECT 'test'||n||'@example.invalid', now()+n*interval '1 second' FROM generate_series(1,10) n", env=env)
    def oldest():
        return run("-v", "count=5", "-f", str(ROOT / "approve-waitlist-oldest.sql"), env=env)
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        outputs = list(pool.map(lambda _: oldest(), range(2)))
    rows = run("-At", "-c", "SELECT count(*) FROM launch_waitlist WHERE approved", env=env).strip()
    assert rows == "10", (rows, outputs)
    run("-c", "UPDATE launch_waitlist SET approved=false", env=env)
    run("-v", "count=1001", "-f", str(ROOT / "approve-waitlist-oldest.sql"), env=env, ok=False)
    assert run("-At", "-c", "SELECT count(*) FROM launch_waitlist WHERE approved", env=env).strip() == "0"
    output = run("-v", "emails=test1@example.invalid,missing@example.invalid", "-f", str(ROOT / "approve-waitlist-emails.sql"), env=env)
    assert output.count("| approved") == 2
    assert run("-At", "-c", "SELECT count(*) FROM launch_waitlist WHERE email='missing@example.invalid'", env=env).strip() == "0"
    output = run("-v", "emails=test1@example.invalid", "-f", str(ROOT / "approve-waitlist-emails.sql"), env=env)
    assert "already approved" in output
    run("-c", "UPDATE launch_waitlist SET approved=false; DELETE FROM signup_approved_emails", env=env)
    def selected():
        return run("-v", "emails=test1@example.invalid", "-f", str(ROOT / "approve-waitlist-emails.sql"), env=env)
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        outputs = list(pool.map(lambda _: selected(), range(2)))
    assert sum("| approved" in line for out in outputs for line in out.splitlines()) == 1, outputs
    assert run("-At", "-c", "SELECT count(*) FROM signup_approved_emails", env=env).strip() == "1"
    print("PASS concurrent oldest and selected batches; invalid limit rolls back; selected email results")
finally:
    run("-c", f'DROP SCHEMA "{schema}" CASCADE')
