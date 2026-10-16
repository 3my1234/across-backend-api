"""CloudShell only: create a runtime secret; never print its contents.

The operator supplies a private JSON config file. This script does not discover
or read local Coolify credentials, change DNS, run migrations or start services.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
from urllib.parse import quote, urlencode

from generate import SECRET_KEYS


def aws(region, *args):
    result = subprocess.run(["aws", "--region", region, *args, "--output", "json"], capture_output=True, text=True)
    if result.returncode:
        # Do not echo command arguments, config or AWS diagnostic payloads.
        raise RuntimeError("AWS operation failed; inspect permissions/account and retry privately")
    return json.loads(result.stdout or "{}")


def postgres_url(host, username, password):
    query = urlencode({"sslmode": "verify-full", "sslrootcert": "/etc/ssl/certs/aws-rds-global.pem"})
    return f"postgres://{quote(username, safe='')}:{quote(password, safe='')}@{host}:5432/across_db?{query}"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--region", required=True)
    parser.add_argument("--foundation", required=True)
    parser.add_argument("--config", required=True, type=Path)
    parser.add_argument("--name", required=True, help="Name of a NEW runtime secret; existing secrets are never overwritten")
    args = parser.parse_args()
    values = json.loads(args.config.read_text(encoding="utf-8"))
    if not isinstance(values, dict): raise ValueError("Private config must be a JSON object")
    for key in SECRET_KEYS:
        if key not in ("DATABASE_URL", "MIGRATION_DATABASE_URL", "REDIS_URL") and not isinstance(values.get(key), str):
            raise ValueError(f"Config requires string key {key}; optional values may be empty strings")
    if len(values.get("JWT_SECRET", "")) < 32: raise ValueError("JWT_SECRET must be the existing strong signing secret (at least 32 characters)")
    outputs = aws(args.region, "cloudformation", "describe-stacks", "--stack-name", args.foundation)["Stacks"][0]["Outputs"]
    outputs = {entry["OutputKey"]: entry["OutputValue"] for entry in outputs}
    database = json.loads(aws(args.region, "secretsmanager", "get-secret-value", "--secret-id", outputs["DatabaseSecretArn"])["SecretString"])
    cache_password = aws(args.region, "secretsmanager", "get-secret-value", "--secret-id", outputs["RedisSecretArn"])["SecretString"]
    url = postgres_url(outputs["DatabaseHost"], database["username"], database["password"])
    values.update(DATABASE_URL=url, MIGRATION_DATABASE_URL=url, REDIS_URL=f"rediss://:{quote(cache_password, safe='')}@{outputs['RedisHost']}:6379/0")
    # Keep secret contents out of argv, shell history, stdout and stack parameters.
    payload = {"Name": args.name, "Description": "Atl xpres private AWS runtime", "SecretString": json.dumps({key: values[key] for key in SECRET_KEYS})}
    path = None
    try:
        with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", delete=False) as handle:
            path = handle.name
            os.chmod(path, 0o600)
            json.dump(payload, handle)
        created = aws(args.region, "secretsmanager", "create-secret", "--cli-input-json", "file://" + path)
        print("Created runtime secret ARN:", created["ARN"])
    finally:
        if path: os.unlink(path)


if __name__ == "__main__":
    try:
        main()
    except Exception:
        raise SystemExit("Runtime secret preparation failed. No secret contents are logged; check inputs and permissions privately.")
