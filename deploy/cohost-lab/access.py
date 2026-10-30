"""Forced SSH command: status/lease/fixture only within this exact run directory.
No shell invocation. Installed only after separate host setup approval.
"""
import json
import base64
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


def setup_verify_command(base,manifest):
    run=manifest.get('run','')
    if not re.fullmatch('cohost-[a-f0-9]{16}',run) or base!=Path('/root/atlxpres-cohost')/run:
        raise ValueError('fixed run directory required')
    if manifest.get('role')!='main' or manifest.get('load_authorized') is not False:
        raise ValueError('setup-only main verification required')
    return ['/usr/bin/python3',str(base/'tools/operator_setup.py'),str(base),'verify']


def main():
    base=Path(sys.argv[1]).resolve()
    if os.name!="posix" or os.geteuid()!=0:raise ValueError("Linux root forced helper required")
    for p in (base,base/"manifest.json"):
        s=p.stat()
        if s.st_uid!=0 or s.st_mode&0o022:raise ValueError("unsafe ownership or permissions")
    command=os.environ.get("SSH_ORIGINAL_COMMAND","")
    if command=='setup-verify':
        import subprocess
        cmd=setup_verify_command(base,json.loads((base/'manifest.json').read_text()))
        subprocess.run(cmd,check=True,timeout=180)
        return
    if command.startswith('upload '):
        from exchange import put
        put(base,command[7:],sys.stdin.buffer)
        return
    if command.startswith('download '):
        from exchange import get
        get(base,command[9:],sys.stdout.buffer)
        return
    if command=='cleanup':
        from operator_setup import clean
        clean(base)
        print('Exact trial cleanup complete',flush=True)
        return
    if command in ('publish-fixture','gateway-relay','observations','observations-full'):
        from trial_host import operation
        operation(base,command)
        return
    if command=='activate':
        from trial_host import activate
        activate(base)
        return
    if command.startswith("control "):
        encoded=command[8:]
        if len(encoded)>100000:raise ValueError("oversized control request")
        operation=json.loads(base64.b64decode(encoded,validate=True))
        from control import run
        raise SystemExit(run(base,operation))
    print(handle(base,command),flush=True)

if __name__=="__main__":main()
