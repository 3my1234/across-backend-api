"""Independent local guard; no peer host, relay or remote heartbeat."""
import json,os,subprocess,sys,time
from pathlib import Path
from runtime import inventory
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'cohost-lab'))
from watchdog import cpu,delta,collect_checks,BLOCKING_URLS,update_health_counters
from safety import reasons,validate_inventory
from baseline import validate as validate_baseline

def observe(base):
    metadata=json.loads((base/'supervisor.json').read_text())
    production=metadata['production_container_ids']
    baseline=validate_baseline(json.loads((base/'baseline.json').read_text()),time.time())
    before=cpu();last=time.monotonic();high_cpu=high_io=0
    failures={u:0 for u in BLOCKING_URLS};slow={u:0 for u in BLOCKING_URLS}
    initial_oom=int(next(x.split()[1] for x in Path('/proc/vmstat').read_text().splitlines() if x.startswith('oom_kill ')))
    next_tick=last+1
    while True:
        time.sleep(max(0,next_tick-time.monotonic()))
        now=time.monotonic();next_tick=now+1;elapsed=now-last;last=now
        after=cpu();busy,wait=delta(before,after);before=after
        high_cpu=high_cpu+elapsed if busy>85 else 0;high_io=high_io+elapsed if wait>10 else 0
        manifest,owned=inventory(base)
        details={};results,states=collect_checks(production+manifest['container_ids'],details)
        update_health_counters(results,baseline,failures,slow)
        mem={k:int(v.split()[0])*1024 for k,v in (row.split(':',1) for row in Path('/proc/meminfo').read_text().splitlines())}
        import shutil
        oom=int(next(x.split()[1] for x in Path('/proc/vmstat').read_text().splitlines() if x.startswith('oom_kill ')))
        sample=dict(available_bytes=mem['MemAvailable'],disk_free_bytes=shutil.disk_usage(base).free,
            oom=oom>initial_oom or any(s.get('OOMKilled') for s in states),
            production_healthy=all(s.get('Running') and s.get('Health',{}).get('Status','healthy')=='healthy' for s in states[:len(production)]),
            cpu_high_seconds=high_cpu,iowait_high_seconds=high_io,health_failures=max(failures.values()),latency_failures=max(slow.values()),
            # No second host exists. This field is a schema compatibility value,
            # never evidence of a remote peer's health; local guard freshness is checked separately.
            peer_heartbeat_age_seconds=0,cpu_busy_percent=busy,iowait_percent=wait)
        (base/'sample.json').write_text(json.dumps(sample))
        with (base/'watchdog.jsonl').open('a') as out:out.write(json.dumps(dict(utc=time.time(),sample=sample,probes=details))+'\n')
        why=reasons(sample,'database')
        if time.time()>=metadata['deadline_unix']:why.append('120-minute hard deadline')
        if why:raise RuntimeError('; '.join(why))
        (base/'local.lease').touch()

def stop_owned(base):
    manifest,rows=inventory(base)
    if manifest['container_ids']:
        validate_inventory(manifest,rows)
        subprocess.run(['docker','stop','--timeout','2',*manifest['container_ids']],check=True,timeout=90)

def main():
    import argparse
    p=argparse.ArgumentParser();p.add_argument('base',type=Path);a=p.parse_args();base=a.base.resolve()
    from runtime import ROOT,identity
    identity(base.name)
    if os.geteuid()!=0 or base.parent!=ROOT:raise ValueError('fixed root-owned test directory required')
    try:observe(base)
    except BaseException as error:
        (base/'local.lease').unlink(missing_ok=True)
        (base/'watchdog-error.json').write_text(json.dumps(dict(utc=time.time(),error_class=type(error).__name__,error=str(error)[:2000])))
        stop_owned(base)
        raise

if __name__=='__main__':main()
