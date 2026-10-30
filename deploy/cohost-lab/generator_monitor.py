"""Actual-generator cgroups and original headroom thresholds; never estimates application capacity."""
import json,subprocess,time
from pathlib import Path

def counters(path):return {k:int(v) for k,v in (line.split() for line in path.read_text().splitlines())}

def sample(process,stop,path):
    deadline=time.monotonic()+30;group=None
    while group is None and process.poll() is None:
        try:
            pid=int(subprocess.check_output(['docker','inspect','--format','{{.State.Pid}}','cohost-generator'],text=True,stderr=subprocess.DEVNULL,timeout=5))
            member=next(x.split('::',1)[1] for x in Path(f'/proc/{pid}/cgroup').read_text().splitlines() if x.startswith('0::'))
            group=(Path('/sys/fs/cgroup')/member.lstrip('/')).resolve()
            if not group.is_relative_to(Path('/sys/fs/cgroup')):raise ValueError('generator cgroup escaped')
        except (OSError,ValueError,StopIteration,subprocess.CalledProcessError):
            if time.monotonic()>deadline:raise RuntimeError('generator cgroup missing')
            time.sleep(.1)
    with path.open('w') as out:
        while group and not stop.is_set():
            began=time.monotonic()
            try:
                row=dict(time_unix=time.time(),cpu=counters(group/'cpu.stat'),memory_current=int((group/'memory.current').read_text()),memory_events=counters(group/'memory.events'),cpu_max=(group/'cpu.max').read_text().strip())
                out.write(json.dumps(row)+'\n');out.flush()
            except FileNotFoundError:break
            stop.wait(max(0,.1-(time.monotonic()-began)))

def percentile(values,fraction):
    ordered=sorted(values)
    if not ordered:raise ValueError('generator samples absent')
    return ordered[min(len(ordered)-1,int((len(ordered)-1)*fraction))]

def resource_gate(samples,begin,end,seconds):
    usage=[];burst=[];covered=0;periods=throttled=0;peak=oom=0
    for old,row in zip(samples,samples[1:]):
        if not begin<=row['time_unix']<=end:continue
        dt=row['time_unix']-old['time_unix']
        if not 0<dt<=1:continue
        quota,period=map(int,row['cpu_max'].split())
        if quota/period!=2:raise ValueError('actual generator quota differs')
        fraction=(row['cpu']['usage_usec']-old['cpu']['usage_usec'])/(dt*1e6*2)
        usage.append(fraction);covered+=dt
        if row['time_unix']<=begin+6:burst.append(fraction)
        periods+=row['cpu'].get('nr_periods',0)-old['cpu'].get('nr_periods',0)
        throttled+=row['cpu'].get('nr_throttled',0)-old['cpu'].get('nr_throttled',0)
        peak=max(peak,row['memory_current']);oom=max(oom,row['memory_events'].get('oom',0)+row['memory_events'].get('oom_kill',0))
    values=dict(sampled_seconds=covered,p95_cpu_quota_fraction=percentile(usage,.95),burst_p95_cpu_quota_fraction=percentile(burst,.95),throttled_period_fraction=throttled/max(1,periods),peak_memory_bytes=peak,oom_events=oom)
    checks=dict(coverage=covered>=seconds-10,cpu_p95=values['p95_cpu_quota_fraction']<.7,burst_cpu_p95=values['burst_p95_cpu_quota_fraction']<.85,throttle=values['throttled_period_fraction']<.01,memory=peak<.7*4*2**30,no_oom=oom==0)
    return dict(values=values,assertions=checks,passed=all(checks.values()))

def gate(directory,seconds,target,profile):
    from datetime import datetime
    stamp=lambda v:datetime.fromisoformat(v.replace('Z','+00:00')).timestamp()
    first=None;last=None;offered=completed=full_vus=0;starts=[]
    with (directory/'timeseries.json').open() as source:
        for line in source:
            item=json.loads(line)
            if item.get('type')!='Point':continue
            data=item['data'];metric=item['metric'];when=stamp(data['time'])
            if metric=='offered_requests':
                offered+=data['value'];first=when if first is None else min(first,when)
            if metric=='http_reqs' and data.get('tags',{}).get('phase')=='measured':completed+=data['value'];last=when if last is None else max(last,when)
            if metric=='vus' and data['value']==target:full_vus+=1
            if metric=='session_starts':starts.append(when)
    if first is None or last is None:raise ValueError('measured generator traffic missing')
    report=resource_gate([json.loads(s) for s in (directory/'generator-cgroups.jsonl').read_text().splitlines()],first,last,seconds)
    summary=json.loads((directory/'summary.json').read_text());count=lambda name:summary['metrics'].get(name,{}).get('values',{}).get('count',0)
    report['assertions'].update(offered_completed=offered==completed,all_authenticated=count('authenticated_sessions')==target and count('warmup_authenticated_sessions')==target,all_started=count('session_starts')==target,local_identity_counts=count('local_generator_prepared')==target and count('local_generator_used')==target,duration=last-first>=seconds-5,no_dropped=count('dropped_iterations')==0)
    if profile!='gradual':report['assertions'].update(full_vus=full_vus>=seconds-10,burst_spread=bool(starts) and max(starts)-min(starts)<=5)
    report.update(offered=offered,completed=completed,measured_seconds=last-first,full_vus_samples=full_vus,passed=all(report['assertions'].values()))
    (directory/'actual-generator-gate.json').write_text(json.dumps(report,indent=2))
    if not report['passed']:raise ValueError('actual generator headroom/accounting gate failed; not an application capacity result')
    return report
