"""Free-CI peer lease relay. Kill generator process group if either watchdog stops.
Caller supplies dedicated ssh_config with pinned host keys and restricted run keys.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import signal
import subprocess
import time


def validate(statuses, previous, run):
    if len(statuses)!=2:raise ValueError("both independent watchdogs required")
    for role,s in zip(("main","database"),statuses):
        if s.get("run")!=run or s.get("role")!=role:raise ValueError("peer identity mismatch")
        age=s.get("age_seconds");sequence=s.get("sequence")
        if type(age) not in (int,float) or not 0<=age<=3:raise ValueError("stale peer")
        if type(sequence) is not int or sequence<=previous.get(role,0):raise ValueError("nonadvancing peer")
    return {s["role"]:s["sequence"] for s in statuses}


def main():
    ap=argparse.ArgumentParser();ap.add_argument("config",type=Path);ap.add_argument("run")
    ap.add_argument("--generator-pgid",type=int,required=True);a=ap.parse_args()
    if a.generator_pgid<=1 or a.generator_pgid==os.getpgrp():raise ValueError("unsafe generator process group")
    previous={}
    def ssh(role,command):
        return subprocess.check_output(["ssh","-F",str(a.config),"cohost-"+role,command],text=True,timeout=2)
    try:
        while True:
            with ThreadPoolExecutor(max_workers=2) as pool:
                statuses=list(pool.map(lambda role:json.loads(ssh(role,"status")),("main","database")))
            previous=validate(statuses,previous,a.run)
            with ThreadPoolExecutor(max_workers=2) as pool:
                list(pool.map(lambda role:ssh(role,"lease"),("main","database")))
            time.sleep(2)
    except BaseException:
        try:os.killpg(a.generator_pgid,signal.SIGTERM)
        except ProcessLookupError:pass
        # Never refresh either peer lease after a failure. Each local watchdog stops owned containers.
        raise

if __name__=="__main__":main()
