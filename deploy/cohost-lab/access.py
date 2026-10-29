"""Forced SSH command: status/lease/fixture only within this exact run directory.
No shell invocation. Installed only after separate host setup approval.
"""
import json
import os
from pathlib import Path
import re
import sys
import time


def handle(base, command):
    manifest=json.loads((base/"manifest.json").read_text())
    if not re.fullmatch(r"cohost-[a-f0-9]{16}",manifest.get("run","")) or base.name!=manifest["run"]:
        raise ValueError("run directory mismatch")
    if command=="status":
        lease=base/"local.lease"
        age=max(0,time.time()-lease.stat().st_mtime) if lease.exists() else 999
        return json.dumps(dict(run=manifest["run"],role=manifest["role"],age_seconds=age,
            sequence=lease.stat().st_mtime_ns if lease.exists() else 0))
    if command=="lease":
        # Trusted relay sends this only after receiving fresh advancing status from BOTH hosts.
        (base/"peer.lease").touch()
        return "lease refreshed"
    if command=="fixture" and manifest.get("role")=="main":
        fixture=base/"exchange"/"fixtures.json"
        if fixture.is_symlink() or fixture.stat().st_size>50*2**20:
            raise ValueError("unsafe fixture file")
        return fixture.read_text()
    raise ValueError("command denied")


def main():
    base=Path(sys.argv[1]).resolve()
    if os.name!="posix" or os.geteuid()!=0:raise ValueError("Linux root forced helper required")
    for p in (base,base/"manifest.json"):
        s=p.stat()
        if s.st_uid!=0 or s.st_mode&0o022:raise ValueError("unsafe ownership or permissions")
    print(handle(base,os.environ.get("SSH_ORIGINAL_COMMAND","")),flush=True)

if __name__=="__main__":main()
