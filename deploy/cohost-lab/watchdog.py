"""Linux observer; never runs unless explicitly invoked by the operator.
Dry-run default. Requires root-owned run directory and independently refreshed
peer lease. Stop mode only delegates to immutable-ID/ownership-checked safety.py.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import math
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
from safety import reasons

URLS = ("https://api.atlxpres.com/api/v1/ready",
        "https://unlimitedenergysystems.com/", "https://atlxpres.com/")


def cpu():
    return list(map(int, Path("/proc/stat").read_text().splitlines()[0].split()[1:9]))


def delta(before, after):
    d=[b-a for a,b in zip(before,after)]; total=sum(d)
    if total <= 0 or min(d) < 0: raise ValueError("invalid CPU observation")
    return 100*(total-d[3]-d[4])/total,100*d[4]/total


def run_probe_process(args, detail):
    """Separate process launch from transfer/wait without changing safety deadlines."""
    launched=time.monotonic()
    with subprocess.Popen(args,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True) as process:
        detail['process_launch_seconds']=time.monotonic()-launched
        waiting=time.monotonic()
        deadline=detail.pop('_observer_deadline',launched+2.5)
        try:
            remaining=deadline-waiting
            if remaining<=0:raise subprocess.TimeoutExpired(args,2.5)
            stdout,stderr=process.communicate(timeout=remaining)
        except subprocess.TimeoutExpired:
            process.kill()
            process.communicate()
            detail['process_timeout']=True
            raise
        finally:
            detail['process_wait_seconds']=time.monotonic()-waiting
        return subprocess.CompletedProcess(args,process.returncode,stdout,stderr)


def probe(url,diagnostics=None):
    start=time.monotonic()
    detail=dict(url=url,_observer_deadline=start+2.5)
    try:
        ready=url.endswith('/ready')
        # curl races dual-stack connections and bounds the COMPLETE transfer.
        # No retries, forced address, relaxed TLS or site-specific exceptions.
        args=['curl','-q','--silent','--show-error','--location','--max-redirs','5',
              '--proto','=https','--proto-redir','=https','--max-time','2',
              '--connect-timeout','2','--user-agent','ATL-Xpress-cohost-safety/1.0',
              '--output','-' if ready else os.devnull,'--write-out',
              '\n%{http_code}\t%{time_namelookup}\t%{time_connect}\t%{time_appconnect}\t%{time_starttransfer}\t%{time_total}\t%{size_download}']
        if ready:args.extend(['--max-filesize','8192'])
        result=run_probe_process(args+[url],detail)
        body,separator,status=result.stdout.rpartition('\n')
        parts=status.strip().split('\t')
        detail.update(exit_code=result.returncode,http_status=parts[0],error=result.stderr.strip()[:500],
            phases=dict(zip(('dns_seconds','connect_seconds','tls_seconds','first_byte_seconds','total_seconds','download_bytes'),parts[1:])))
        ok=result.returncode==0 and bool(separator) and parts[0]=='200'
        if ready:ok=ok and len(body.encode())<=8192 and json.loads(body).get('ok') is True
        if len(parts)!=7:raise ValueError('complete HTTP timing metadata required')
        http_seconds=float(parts[5]);observer_seconds=time.monotonic()-start
        if not math.isfinite(http_seconds) or http_seconds<=0:raise ValueError('invalid HTTP transfer duration')
        timing_ok=http_seconds<=2 and math.isfinite(observer_seconds) and 0<=observer_seconds<=2.5
        detail.update(transport_healthy=ok,healthy=ok and timing_ok,http_seconds=http_seconds,observer_seconds=observer_seconds,seconds=observer_seconds,timing_policy='http-2s-observer-2.5s-v1')
        if 'process_wait_seconds' in detail:
            detail['wait_outside_transfer_seconds']=max(0,detail['process_wait_seconds']-http_seconds)
        return url,detail['healthy'],http_seconds
    except Exception as error:
        detail.update(healthy=False,error_class=type(error).__name__,seconds=time.monotonic()-start)
        return url,False,detail['seconds']
    finally:
        detail.pop('_observer_deadline',None)
        if diagnostics is not None:diagnostics[url]=detail


def state(ids):
    if not ids: raise ValueError("production inventory missing")
    rows=subprocess.run(["docker","inspect","--format","{{json .State}}",*ids],
        check=True,capture_output=True,text=True,timeout=3).stdout.splitlines()
    if len(rows)!=len(ids): raise ValueError("state inventory incomplete")
    return [json.loads(x) for x in rows]


def approved_trial_transition(base,current):
    """Keep the existing independent observer alive across an explicitly approved arm."""
    from trial_policy import validate
    approval=base/'trial-approval.json'
    for path in (approval,base/'trial-started'):
        info=path.stat()
        if info.st_uid!=0 or info.st_mode&0o022:raise ValueError('unsafe trial transition ownership')
    validate(json.loads(approval.read_text()),current['run'])
    if current.get('fixture_preparation_authorized') is not True or not 0<current.get('trial_deadline_unix',0)-time.time()<=7200:
        raise ValueError('bounded explicit trial transition required')


def main():
    ap=argparse.ArgumentParser();ap.add_argument("manifest",type=Path)
    ap.add_argument("--stop-test",action="store_true")
    ap.add_argument("--setup-only",action="store_true");a=ap.parse_args()
    m=json.loads(a.manifest.read_text());base=a.manifest.parent
    import re
    if m.get("role") not in ("main","database") or not re.fullmatch(r"cohost-[a-f0-9]{16}",m.get("run","")):
        raise ValueError("invalid manifest")
    ids=m.get("production_container_ids",[])
    if not ids or any(not re.fullmatch(r"[a-f0-9]{64}",x) for x in ids): raise ValueError("invalid production inventory")
    from baseline import validate as validate_baseline
    baseline=validate_baseline(json.loads((base/"baseline.json").read_text()),time.time())
    if a.setup_only and m.get("load_authorized") is not False:raise ValueError("setup-only guard requires load disabled")
    started=time.monotonic()
    before=cpu();last=time.monotonic();high_cpu=high_io=0
    failures={u:0 for u in URLS};slow={u:0 for u in URLS}
    initial_oom=int(next(x.split()[1] for x in Path("/proc/vmstat").read_text().splitlines() if x.startswith("oom_kill ")))
    next_tick=time.monotonic()+1
    try:
        while True:
            # Target one-second sampling, rather than adding a full second to
            # every completed probe/inspect cycle. Never publish during a scan.
            time.sleep(max(0,next_tick-time.monotonic()))
            now=time.monotonic();next_tick=now+1;elapsed=now-last;last=now
            after=cpu();busy,wait=delta(before,after);before=after
            high_cpu=high_cpu+elapsed if busy>85 else 0
            high_io=high_io+elapsed if wait>10 else 0
            mem={k:int(v.split()[0])*1024 for k,v in (x.split(":",1) for x in Path("/proc/meminfo").read_text().splitlines())}
            probe_details={}
            with ThreadPoolExecutor(max_workers=4) as pool:
                results=list(pool.map(lambda url:probe(url,probe_details),URLS))
            for url,ok,latency in results:
                failures[url]=0 if ok else failures[url]+1
                slow[url]=slow[url]+1 if latency>max(2,3*baseline[url]) else 0
            current=json.loads(a.manifest.read_text())
            if current.get("run")!=m["run"] or current.get("role")!=m["role"] or current.get("production_container_ids")!=ids:
                raise ValueError("protected inventory changed")
            if a.setup_only and current.get('load_authorized') is not False:
                approved_trial_transition(base,current)
                a.setup_only=False
            m=current
            states=state(ids+m.get("container_ids",[]))
            prod=states[:len(ids)]
            oom=int(next(x.split()[1] for x in Path("/proc/vmstat").read_text().splitlines() if x.startswith("oom_kill ")))
            peer=base/"peer.lease"
            sample=dict(available_bytes=mem["MemAvailable"],disk_free_bytes=shutil.disk_usage(base).free,
                oom=oom>initial_oom or any(x.get("OOMKilled") for x in states),
                production_healthy=all(x.get("Running") and x.get("Health",{}).get("Status","healthy")=="healthy" for x in prod),
                cpu_high_seconds=high_cpu,iowait_high_seconds=high_io,cpu_busy_percent=busy,iowait_percent=wait,
                health_failures=max(failures.values()),latency_failures=max(slow.values()),
                peer_heartbeat_age_seconds=(0 if time.monotonic()-started<900 else 999) if a.setup_only else (max(0,time.time()-peer.stat().st_mtime) if peer.exists() else 999))
            (base/"sample.json").write_text(json.dumps(sample))
            with (base/"watchdog.jsonl").open("a") as log:
                log.write(json.dumps(dict(utc=time.time(),sample=sample,probes=probe_details))+"\n")
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
