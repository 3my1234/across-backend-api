"""Conservative independent-generator gate; never an application-capacity claim."""
import json
from pathlib import Path
import sys

def percentile(values, fraction):
    ordered=sorted(values)
    return ordered[min(len(ordered)-1,int((len(ordered)-1)*fraction))] if ordered else None

def reviewed_profile(configuration):
    profile=configuration.get('profile')
    seconds={'stage':180,'sustained':1800}.get(profile)
    if seconds is None or configuration.get('seconds')!=seconds or configuration.get('target')!=500:raise ValueError('Unexpected reviewed500-session profile or duration')
    return profile,seconds

def memory_headroom(peak, declared, actual):
    if declared not in (2*1024**3,4*1024**3) or actual!=declared:
        raise ValueError('Generator memory allocation does not match the explicit reviewed budget')
    return peak < .7*declared


def analyze(directory, quota, memory_bytes=2*1024**3):
    root=Path(directory)
    summary=json.loads((root/'summary.json').read_text())
    metrics=summary['metrics']
    configuration=summary['configuration']
    profile,requested_seconds=reviewed_profile(configuration)
    count=lambda name: metrics.get(name,{}).get('values',{}).get('count',0)
    failures=[f'{name}:{label}' for name,value in metrics.items() for label,state in value.get('thresholds',{}).items() if not state.get('ok',False)]
    observed=[];started=[];ended=[];vus500=[]
    with (root/'timeseries.json').open() as stream:
        for line in stream:
            item=json.loads(line)
            if item.get('type')!='Point':continue
            data=item['data'];tags=data.get('tags',{})
            if item.get('metric')=='offered_requests':observed.append(float(data['value']))
            if item.get('metric')=='http_reqs' and tags.get('phase')=='measured':ended.append(data['time'])
            if item.get('metric')=='offered_requests':started.append(data['time'])
            if item.get('metric')=='vus' and data['value']==500:vus500.append(data['time'])
    from datetime import datetime
    stamp=lambda value:datetime.fromisoformat(value.replace('Z','+00:00')).timestamp()
    begin=min(map(stamp,started));end=max(map(stamp,ended))
    samples=[json.loads(s) for s in (root/'cgroups.jsonl').read_text().splitlines() if s.strip()]
    usage={};previous={};memory={};oom={};intervals=[]
    for row in samples:
        now=row['time_unix']
        for name,item in row['containers'].items():
            if name not in previous:previous[name]=(now,item);continue
            oldtime,old=previous[name];previous[name]=(now,item)
            if now<begin or now>end:continue
            duration=now-oldtime
            if duration<=0 or duration>1:continue
            cpu=(item['cpu']['usage_usec']-old['cpu']['usage_usec'])/(duration*1e6)
            throttle=(item['cpu'].get('nr_throttled',0)-old['cpu'].get('nr_throttled',0))
            periods=item['cpu'].get('nr_periods',0)-old['cpu'].get('nr_periods',0)
            usage.setdefault(name,[]).append({'cores':cpu,'duration':duration,'throttled':throttle,'periods':periods,'burst':now<=begin+6})
            memory[name]=max(memory.get(name,0),item['memory_current'])
            oom[name]=max(oom.get(name,0),item['memory_events'].get('oom',0)+item['memory_events'].get('oom_kill',0))
            intervals.append(duration)
    result={}
    for name,rows in usage.items():
        is_generator='sessions' in name;limit=quota if is_generator else 1
        total=sum(r['duration'] for r in rows)
        result[name]={'quota_cores':limit,'sample_count':len(rows),'sampled_seconds':total,'mean_cpu_quota_fraction':sum(r['cores']*r['duration'] for r in rows)/total/limit,'p95_cpu_quota_fraction':percentile([r['cores']/limit for r in rows],.95),'burst_p95_cpu_quota_fraction':percentile([r['cores']/limit for r in rows if r['burst']],.95),'throttled_period_fraction':sum(r['throttled'] for r in rows)/max(1,sum(r['periods'] for r in rows)),'peak_memory_bytes':memory[name],'oom_events':oom[name]}
    generators=[r for name,r in result.items() if 'sessions' in name]
    targets=[r for name,r in result.items() if 'gateway' in name]
    if len(generators)!=1 or len(targets)!=1:raise ValueError('Missing unambiguous generator/target telemetry')
    generator=generators[0];target=targets[0]
    inspected=json.loads((root/'generator-inspect.json').read_text())[0]
    actual_memory=inspected['HostConfig']['Memory']
    stats=json.loads((root/'target-stats.json').read_text())
    assertions={
      'k6_exit_zero':int((root/'exit-code.txt').read_text())==0,
      'original_thresholds_pass':not failures,
      'all_500_authenticated':count('authenticated_sessions')==500 and count('warmup_authenticated_sessions')==500,
      'all_500_started':count('session_starts')==500 and stats['active_identities']==500,
      'requested_duration_measured':end-begin>=requested_seconds-5,
      'all_500_vus_sustained':sum(begin<=stamp(t)<=end for t in vus500)>=requested_seconds-10,
      'local_preparation_identity_counts':profile!='sustained' or (count('local_generator_prepared')==500 and count('local_generator_used')==500),
      'offered_completed_accounting':sum(observed)==len(ended),
      'mock_received_every_request':stats['requests']==len(ended)+1004,
      'no_dropped_iterations':count('dropped_iterations')==0,
      'burst_spread_under_5_seconds':stats['first_activity_spread_seconds']<=5,
      'generator_sample_coverage':generator['sampled_seconds']>=requested_seconds-10,
      'generator_cpu_p95_under_70pct':generator['p95_cpu_quota_fraction']<.70,
      'generator_burst_cpu_p95_under_85pct':generator['burst_p95_cpu_quota_fraction'] is not None and generator['burst_p95_cpu_quota_fraction']<.85,
      'generator_throttle_periods_under_1pct':generator['throttled_period_fraction']<.01,
      'generator_memory_under_70pct':memory_headroom(generator['peak_memory_bytes'],memory_bytes,actual_memory),
      'generator_no_oom':generator['oom_events']==0,
      'mock_cpu_p95_under_70pct':target['p95_cpu_quota_fraction']<.70,
      'mock_no_oom':target['oom_events']==0,
    }
    return {'generator_memory_limit_bytes':memory_bytes,'independent_generator_gate_passed':all(assertions.values()),'assertions':assertions,'failed_original_thresholds':failures,'offered':sum(observed),'completed':len(ended),'measured_seconds':end-begin,'requested_seconds':requested_seconds,'vus500_samples':sum(begin<=stamp(t)<=end for t in vus500),'http_per_second':len(ended)/(end-begin),'target':stats,'containers':result,'sample_interval_p99_seconds':percentile(intervals,.99),'scope':'Unchanged 500-VU session actions against synthetic stateful HTTP; no application/database/worker capacity claim. Does not prove shared-host headroom during actual qualification.'}

if __name__=='__main__':
    output=analyze(sys.argv[1],float(sys.argv[2]),int(sys.argv[3]) if len(sys.argv)>3 else 2*1024**3);Path(sys.argv[1],'gate.json').write_text(json.dumps(output,indent=2));print(json.dumps(output,indent=2))
    sys.exit(0 if output['independent_generator_gate_passed'] else 1)
