"""Linux observer; never runs unless explicitly invoked by the operator.
Dry-run default. Requires root-owned run directory and independently refreshed
peer lease. Stop mode only delegates to immutable-ID/ownership-checked safety.py.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
import urllib.request
from safety import reasons

URLS = ("https://api.atlxpres.com/api/v1/ready",
        "https://samnadoilandgas.sportbanter.online/",
        "https://unlimitedenergysystems.com/", "https://atlxpres.com/")


def cpu():
    return list(map(int, Path("/proc/stat").read_text().splitlines()[0].split()[1:9]))


def delta(before, after):
    d=[b-a for a,b in zip(before,after)]; total=sum(d)
    if total <= 0 or min(d) < 0: raise ValueError("invalid CPU observation")
    return 100*(total-d[3]-d[4])/total,100*d[4]/total


def probe(url):
    start=time.monotonic()
    try:
        request=urllib.request.Request(url,headers={"User-Agent":"ATL-Xpress-cohost-safety/1.0"})
        with urllib.request.urlopen(request,timeout=2) as response:
            body=response.read(8192)
            ok=response.status==200
            if url.endswith("/ready"): ok=ok and json.loads(body).get("ok") is True
        return url,ok,time.monotonic()-start
    except Exception:
        return url,False,time.monotonic()-start


def state(ids):
    if not ids: raise ValueError("production inventory missing")
    rows=subprocess.run(["docker","inspect","--format","{{json .State}}",*ids],
        check=True,capture_output=True,text=True,timeout=3).stdout.splitlines()
    if len(rows)!=len(ids): raise ValueError("state inventory incomplete")
    return [json.loads(x) for x in rows]


def main():
    ap=argparse.ArgumentParser();ap.add_argument("manifest",type=Path)
    ap.add_argument("--stop-test",action="store_true");a=ap.parse_args()
    m=json.loads(a.manifest.read_text());base=a.manifest.parent
    import re
    if m.get("role") not in ("main","database") or not re.fullmatch(r"cohost-[a-f0-9]{16}",m.get("run","")):
        raise ValueError("invalid manifest")
    ids=m.get("production_container_ids",[])
    if not ids or any(not re.fullmatch(r"[a-f0-9]{64}",x) for x in ids): raise ValueError("invalid production inventory")
    baseline=json.loads((base/"baseline.json").read_text())
    if set(baseline)!=set(URLS) or any(not 0<v<=2 for v in baseline.values()):
        raise ValueError("five-minute successful baseline required")
    before=cpu();last=time.monotonic();high_cpu=high_io=0
    failures={u:0 for u in URLS};slow={u:0 for u in URLS}
    initial_oom=int(next(x.split()[1] for x in Path("/proc/vmstat").read_text().splitlines() if x.startswith("oom_kill ")))
    try:
        while True:
            time.sleep(1);now=time.monotonic();elapsed=now-last;last=now
            after=cpu();busy,wait=delta(before,after);before=after
            high_cpu=high_cpu+elapsed if busy>85 else 0
            high_io=high_io+elapsed if wait>10 else 0
            mem={k:int(v.split()[0])*1024 for k,v in (x.split(":",1) for x in Path("/proc/meminfo").read_text().splitlines())}
            with ThreadPoolExecutor(max_workers=4) as pool: results=list(pool.map(probe,URLS))
            for url,ok,latency in results:
                failures[url]=0 if ok else failures[url]+1
                slow[url]=slow[url]+1 if latency>max(2,3*baseline[url]) else 0
            states=state(ids+m.get("container_ids",[]))
            prod=states[:len(ids)]
            oom=int(next(x.split()[1] for x in Path("/proc/vmstat").read_text().splitlines() if x.startswith("oom_kill ")))
            peer=base/"peer.lease"
            sample=dict(available_bytes=mem["MemAvailable"],disk_free_bytes=shutil.disk_usage(base).free,
                oom=oom>initial_oom or any(x.get("OOMKilled") for x in states),
                production_healthy=all(x.get("Running") and x.get("Health",{}).get("Status","healthy")=="healthy" for x in prod),
                cpu_high_seconds=high_cpu,iowait_high_seconds=high_io,
                health_failures=max(failures.values()),latency_failures=max(slow.values()),
                peer_heartbeat_age_seconds=max(0,time.time()-peer.stat().st_mtime) if peer.exists() else 999)
            (base/"sample.json").write_text(json.dumps(sample))
            with (base/"watchdog.jsonl").open("a") as log: log.write(json.dumps(dict(utc=time.time(),sample=sample))+"\n")
            why=reasons(sample,m["role"])
            if why:
                print(json.dumps(dict(abort=why)),flush=True)
                command=[sys.executable,str(Path(__file__).with_name("safety.py")),str(a.manifest),str(base/"sample.json")]
                if a.stop_test: command.append("--stop-test")
                subprocess.run(command,check=False,timeout=120)
                raise SystemExit(2)
            (base/"local.lease").touch()
    except BaseException:
        # Peer must treat cessation of this lease as failure and stop its own test.
        (base/"local.lease").unlink(missing_ok=True)
        if a.stop_test:
            (base/"sample.json").write_text("{}")
            subprocess.run([sys.executable,str(Path(__file__).with_name("safety.py")),str(a.manifest),str(base/"sample.json"),"--stop-test"],check=False,timeout=120)
        raise

if __name__=="__main__":main()
