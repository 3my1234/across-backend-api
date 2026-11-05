"""Five-minute public safety baseline; no load generation or application writes."""
import argparse,json,time
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
from watchdog import URLS,probe

POLICY="http-2s-observer-2.5s-v1"
DNS_POLICY='dns-only-3-attempts-12s-v1'

def dns_failure(detail):
    return (detail.get('http_status')=='000' and
        (detail.get('exit_code')==6 or (detail.get('exit_code')==28 and 'Resolving timed out' in detail.get('error',''))))

def baseline_probe(url,diagnostics):
    """Only pre-load DNS failures get retries; every HTTP attempt retains its limits."""
    deadline=time.monotonic()+12;attempts=[];result=None
    for index in range(3):
        if index and time.monotonic()+2.5>deadline:break
        details={};result=probe(url,details);detail=details.get(url,{})
        attempts.append(detail)
        if not dns_failure(detail):break
        if index<2:
            print(json.dumps(dict(phase='baseline DNS retry',url=url,attempt=index+1,deadline_seconds=12)),flush=True)
            time.sleep(1)
    diagnostics[url]={**attempts[-1], 'dns_retry_policy':DNS_POLICY,'dns_failures':sum(dns_failure(d) for d in attempts),'attempts':attempts}
    return result

def validate(doc,now):
    if doc.get("timing_policy")!=POLICY:raise ValueError("approved split timing policy evidence required")
    if doc.get('observed_seconds',0)<300 or not 0<=now-doc.get('finished_unix',0)<=900:
        raise ValueError('fresh five-minute baseline required')
    rows=doc.get('urls',{})
    if set(rows)!=set(URLS):raise ValueError('all protected sites required')
    for value in rows.values():
        if value.get('successful_samples',0)<60 or not 0<value.get('p95_seconds',0)<=2 or not 0<value.get('observer_p95_seconds',0)<=2.5:
            raise ValueError('baseline evidence incomplete/unhealthy')
    return {url:rows[url]['p95_seconds'] for url in URLS}

def main():
    ap=argparse.ArgumentParser();ap.add_argument('output',type=Path);a=ap.parse_args()
    values={u:[] for u in URLS};observers={u:[] for u in URLS};start=time.monotonic()
    started_unix=time.time()
    heartbeat=start
    print('Five-minute pre-load baseline starting; DNS-only retries are bounded. No load or test containers started.',flush=True)
    while time.monotonic()-start<300 or min(map(len,values.values()))<60:
        details={}
        with ThreadPoolExecutor(max_workers=4) as pool:results=list(pool.map(lambda url:baseline_probe(url,details),URLS))
        with a.output.with_name('baseline-observations.jsonl').open('a') as log:
            log.write(json.dumps(dict(baseline_started_unix=started_unix,collected_unix=time.time(),probes=details))+'\n')
        for url,ok,seconds in results:
            if not ok or seconds>2:
                failure=dict(baseline='failed',url=url,healthy=ok,seconds=seconds,
                    completed_samples={u:len(v) for u,v in values.items()},load_started=False,probe=details.get(url))
                a.output.with_name('baseline-failure.json').write_text(json.dumps(failure,indent=2))
                print(json.dumps(failure),flush=True)
                raise ValueError('baseline health failure; do not arm test')
            values[url].append(seconds)
            observers[url].append(details[url]['observer_seconds'])
        time.sleep(5)
        if time.monotonic()-heartbeat>=30:
            print(json.dumps(dict(phase='baseline',elapsed_seconds=round(time.monotonic()-start),successful_samples={u:len(v) for u,v in values.items()},load_started=False)),flush=True);heartbeat=time.monotonic()
    doc=dict(timing_policy=POLICY,dns_retry_policy=DNS_POLICY,observed_seconds=time.monotonic()-start,finished_unix=time.time(),urls={
        url:dict(successful_samples=len(v),p95_seconds=sorted(v)[int((len(v)-1)*.95)],observer_p95_seconds=sorted(observers[url])[int((len(v)-1)*.95)]) for url,v in values.items()})
    validate(doc,time.time());a.output.write_text(json.dumps(doc,indent=2))
    print('Five-minute baseline passed; no load authorized.')
if __name__=='__main__':main()
