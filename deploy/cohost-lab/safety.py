"""Cohost trial safety policy. No remote access; dry-run unless --stop-test is explicit.
Manifest must be root-owned on Linux before an operator enables stop actions.
Only immutable container IDs carrying this run's ownership label may be stopped.
"""
import argparse
import json
import math
import os
from pathlib import Path
import re
import stat
import subprocess

LABEL = "atlxpres.cohost.run"


def reasons(sample, role):
    if role not in ("main", "database"):
        return ["invalid role"]
    floor = (30 if role == "main" else 20) * 2**30
    result = []
    required = ("available_bytes", "disk_free_bytes", "oom", "production_healthy",
                "cpu_high_seconds", "iowait_high_seconds", "health_failures",
                "latency_failures", "peer_heartbeat_age_seconds")
    if any(k not in sample for k in required):
        return ["incomplete monitoring sample"]
    for key in required:
        value = sample[key]
        if key in ("oom", "production_healthy"):
            if type(value) is not bool: return ["invalid monitoring sample"]
        elif type(value) not in (int, float) or not math.isfinite(value) or value < 0:
            return ["invalid monitoring sample"]
    if sample["available_bytes"] < 2 * 2**30: result.append("memory reserve")
    if sample["disk_free_bytes"] < floor: result.append("disk reserve")
    if sample["oom"]: result.append("OOM")
    if not sample["production_healthy"]: result.append("production health")
    if sample["cpu_high_seconds"] >= 5: result.append("CPU above 85 percent")
    if sample["iowait_high_seconds"] >= 5: result.append("IO wait above 10 percent")
    if sample["health_failures"] >= 2: result.append("public health")
    if sample["latency_failures"] >= 3: result.append("public latency")
    if sample["peer_heartbeat_age_seconds"] > 5: result.append("peer heartbeat")
    return result


def validate_inventory(manifest, inspect):
    nonce = manifest.get("run", "")
    ids = manifest.get("container_ids", [])
    if not re.fullmatch(r"cohost-[a-f0-9]{16}", nonce):
        raise ValueError("invalid run nonce")
    if not ids or len(set(ids)) != len(ids):
        raise ValueError("empty or duplicate inventory")
    if any(not re.fullmatch(r"[a-f0-9]{64}", x) for x in ids):
        raise ValueError("immutable full container IDs required")
    records = {x["Id"]: x for x in inspect}
    if set(records) != set(ids):
        raise ValueError("inspect inventory mismatch")
    for ident in ids:
        if records[ident].get("Config", {}).get("Labels", {}).get(LABEL) != nonce:
            raise ValueError("ownership label mismatch; refuse mutation")
    return ids


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("manifest", type=Path)
    ap.add_argument("sample", type=Path)
    ap.add_argument("--stop-test", action="store_true")
    a = ap.parse_args()
    manifest = json.loads(a.manifest.read_text())
    role = manifest.get("role")
    if role not in ("main", "database"): raise ValueError("invalid role")
    why = reasons(json.loads(a.sample.read_text()), role)
    print(json.dumps({"abort_reasons": why, "mutation_enabled": a.stop_test}))
    if not why: return
    if not a.stop_test: raise SystemExit(2)
    if os.name != "posix" or os.geteuid() != 0:
        raise ValueError("stop mode requires Linux root")
    info = a.manifest.stat()
    if info.st_uid != 0 or info.st_mode & (stat.S_IWGRP | stat.S_IWOTH):
        raise ValueError("unsafe manifest ownership/mode")
    # Validate syntax before even invoking Docker; then inspect all before stopping any.
    ids = manifest.get("container_ids", [])
    if not ids or any(not re.fullmatch(r"[a-f0-9]{64}", x) for x in ids):
        raise ValueError("immutable IDs required")
    inspected = subprocess.run(["docker", "inspect", *ids], check=True,
                               capture_output=True, text=True, timeout=10)
    ids = validate_inventory(manifest, json.loads(inspected.stdout))
    for ident in ids:
        subprocess.run(["docker", "stop", "--time", "2", ident], check=True, timeout=10)
    raise SystemExit(2)

if __name__ == "__main__": main()
