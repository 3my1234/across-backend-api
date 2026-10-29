"""Five-minute public safety baseline; no load generation or application writes."""
import argparse,json,time
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
from watchdog import URLS,probe

def validate(doc,now):
    if doc.get('observed_seconds',0)<300 or not 0<=now-doc.get('finished_unix',0)<=900:
        raise ValueError('fresh five-minute baseline required')
    rows=doc.get('urls',{})
    if set(rows)!=set(URLS):raise ValueError('all protected sites required')
    for value in rows.values():
        if value.get('successful_samples',0)<60 or not 0<value.get('p95_seconds',0)<=2:
            raise ValueError('baseline evidence incomplete/unhealthy')
    return {url:rows[url]['p95_seconds'] for url in URLS}

def main():
    ap=argparse.ArgumentParser();ap.add_argument('output',type=Path);a=ap.parse_args()
    values={u:[] for u in URLS};start=time.monotonic()
    while time.monotonic()-start<300 or min(map(len,values.values()))<60:
        with ThreadPoolExecutor(max_workers=4) as pool:results=list(pool.map(probe,URLS))
        for url,ok,seconds in results:
            if not ok or seconds>2:
                failure=dict(baseline='failed',url=url,healthy=ok,seconds=seconds,
                    completed_samples={u:len(v) for u,v in values.items()},load_started=False)
                a.output.with_name('baseline-failure.json').write_text(json.dumps(failure,indent=2))
                print(json.dumps(failure),flush=True)
                raise ValueError('baseline health failure; do not arm test')
            values[url].append(seconds)
        time.sleep(5)
    doc=dict(observed_seconds=time.monotonic()-start,finished_unix=time.time(),urls={
        url:dict(successful_samples=len(v),p95_seconds=sorted(v)[int((len(v)-1)*.95)]) for url,v in values.items()})
    validate(doc,time.time());a.output.write_text(json.dumps(doc,indent=2))
    print('Five-minute baseline passed; no load authorized.')
if __name__=='__main__':main()
