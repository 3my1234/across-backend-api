"""Passive existing-host assessment. No SQL, stress, network throughput test or mutations."""
import argparse
from datetime import datetime, timezone
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time

def command(args):
    return subprocess.check_output(args, timeout=20, stderr=subprocess.DEVNULL).decode()

def cpu():
    return {parts[0]:list(map(int,parts[1:9])) for line in Path('/proc/stat').read_text().splitlines()
            if (parts:=line.split()) and re.fullmatch(r'cpu\d*',parts[0])}

def cpu_delta(before, after):
    delta=[b-a for a,b in zip(before,after)]
    total=sum(delta)
    if total<=0:return {'busy_pct':0,'iowait_pct':0,'steal_pct':0}
    return {'busy_pct':100*(total-delta[3]-delta[4])/total,
            'iowait_pct':100*delta[4]/total,'steal_pct':100*delta[7]/total}

def memory():
    values={k:int(v.split()[0])*1024 for k,v in
            (line.split(':',1) for line in Path('/proc/meminfo').read_text().splitlines())}
    return {k:values[k] for k in ('MemTotal','MemAvailable','SwapTotal','SwapFree','Dirty')}

def counters(file):
    return Path(file).read_text().splitlines()

def io_snapshot():
    disk={}
    for line in counters('/proc/diskstats'):
        p=line.split()
        if len(p)>=14 and not p[2].startswith(('loop','ram')):
            disk[p[2]]={'read_sectors':int(p[5]),'write_sectors':int(p[9]),'io_busy_ms':int(p[12])}
    net={}
    for line in counters('/proc/net/dev')[2:]:
        name,fields=line.split(':');name=name.strip();p=fields.split()
        if not name.startswith(('lo','veth','br-','docker')):
            net[name]={'rx_bytes':int(p[0]),'tx_bytes':int(p[8]),'rx_drops':int(p[3]),'tx_drops':int(p[11])}
    return {'disk':disk,'network':net}

def container_metadata(row, alias):
    h=row['Limits'];state=row['State'];labels=row.get('Labels') or {}
    domains=set()
    for key,value in labels.items():
        if key.startswith('traefik.http.routers.') and key.endswith('.rule'):
            for rule in re.findall(r'Host\(([^)]*)\)',value):
                for host in re.findall(r'[`\"]([a-zA-Z0-9.-]+)[`\"]',rule):
                    if '.' in host and not host.endswith(('.local','.invalid')):
                        try:ipaddress.ip_address(host)
                        except ValueError:domains.add(host)
    return {'alias':alias,'running':state.get('Running'),'health':state.get('Health',{}).get('Status','not_configured'),
            'oom_killed':state.get('OOMKilled'), 'cpuset':h.get('CpusetCpus',''),
            'nano_cpus':h.get('NanoCpus',0),'cpu_quota':h.get('CpuQuota',0),'cpu_period':h.get('CpuPeriod',0),
            'cpu_shares':h.get('CpuShares',0),'memory_limit_bytes':h.get('Memory',0),
            'pids_limit':h.get('PidsLimit'),'public_route_domains':sorted(domains)}

def inventory():
    rows=command(['docker','ps','--format','{{.ID}} {{.Names}}']).splitlines()
    result=[];aliases={}
    for index,line in enumerate(rows):
        identifier,name=line.split(maxsplit=1)
        role='other-service'
        for prefix,label in [('uhvmflj3j60wmnwpbza1bnaf','production-postgres'),
              ('x139kasktvpelpef1t6xgid3-','original-api'),('sx426mx9d9ugbeouvhk4dunj-','worker'),
              ('atlxpres-scale-pilot-api-','api-replica'),('chb0s8dg2bwiug9jjix2lwc1-','admin'),
              ('kunh84s025zaq9ybo2g6mhdu-','provider-web')]:
            if prefix in name:role=label;break
        alias=f'{role}-{index+1}';aliases[name]=alias
        # Deliberately does not retrieve Config.Env, arguments or mount contents.
        template='{"Limits":{{json .HostConfig}},"State":{{json .State}},"Labels":{{json .Config.Labels}}}'
        row=json.loads(command(['docker','inspect','--format',template,identifier]))
        result.append(container_metadata(row,alias))
    return result,aliases

def stats(aliases):
    raw=command(['docker','stats','--no-stream','--format','{{json .}}'])
    return [{'alias':aliases.get(x.get('Name'),'unclassified-service'),
             **{k:x.get(k) for k in ('CPUPerc','MemUsage','MemPerc','BlockIO','NetIO','PIDs')}}
            for line in raw.splitlines() if (x:=json.loads(line))]

def percentile(values,q):
    ordered=sorted(values);return ordered[min(len(ordered)-1,int((len(ordered)-1)*q))]

def run(role,seconds):
    assert os.name=='posix' and os.geteuid()==0 and 10<=seconds<=120
    report={'role':role,'collected_utc':datetime.now(timezone.utc).isoformat(),
            'services_changed':False,'settings_changed':False,'database_queries':False,
            'stress_test_executed':False,'errors':[]}
    report['visible_cpus']=os.cpu_count()
    report['cpu_model']=next((x.split(':',1)[1].strip() for x in Path('/proc/cpuinfo').read_text().splitlines() if x.startswith('model name')),'unknown')
    report['memory_before']=memory();report['load_average']=list(os.getloadavg())
    report['filesystem']=dict(zip(('total','used','free'),shutil.disk_usage('/data/coolify' if Path('/data/coolify').is_dir() else '/')))
    try:
        report['containers'],aliases=inventory();report['containers_before']=stats(aliases)
        expected='production-postgres' if role=='database' else 'original-api'
        assert any(x['alias'].startswith(expected) for x in report['containers']), 'Wrong host role'
    except Exception as e:
        report['errors'].append({'stage':'container_inventory','error_class':type(e).__name__});aliases={}
    start=time.monotonic();before_io=io_snapshot();previous=cpu();samples=[]
    while time.monotonic()-start<seconds:
        time.sleep(2);current=cpu()
        samples.append({k:cpu_delta(previous[k],current[k]) for k in previous.keys() & current.keys()});previous=current
    elapsed=time.monotonic()-start;after_io=io_snapshot()
    report['observed_seconds']=elapsed
    report['cpu_observation']={key:{metric:{'p50':percentile([s[key][metric] for s in samples],.5),
                    'p95':percentile([s[key][metric] for s in samples],.95),'max':max(s[key][metric] for s in samples)}
                    for metric in ('busy_pct','iowait_pct','steal_pct')} for key in samples[-1]}
    report['io_counter_deltas_per_second']={kind:{name:{k:(values[k]-before_io[kind][name][k])/elapsed for k in values}
                  for name,values in after_io[kind].items() if name in before_io[kind]} for kind in before_io}
    report['memory_after']=memory()
    if aliases:
        try:report['containers_after']=stats(aliases)
        except Exception as e:report['errors'].append({'stage':'container_stats','error_class':type(e).__name__})
    report['collection']='complete' if not report['errors'] else 'incomplete'
    report['limitations']='Short passive observation, not peak-capacity proof. Disk devices can overlap; do not sum layered counters. No bandwidth/IO stress, database data, credentials or environment values collected. Co-hosted testing still shares kernel/storage resources and needs a separately approved plan and live abort watchdog.'
    return report

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--role',choices=['main','database'],required=True);p.add_argument('--seconds',type=int,default=60);args=p.parse_args()
    try:report=run(args.role,args.seconds)
    except Exception as e:report={'role':args.role,'collection':'incomplete','error_class':type(e).__name__,'services_changed':False,'settings_changed':False}
    print(json.dumps(report,indent=2))
    raise SystemExit(0 if report['collection']=='complete' else 1)
