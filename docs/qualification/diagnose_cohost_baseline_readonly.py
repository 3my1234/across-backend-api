"""Bounded read-only public health and affected-site container metadata. No changes."""
import concurrent.futures,json,subprocess,time,urllib.request,urllib.error
from pathlib import Path

URLS=('https://api.atlxpres.com/api/v1/ready','https://samnadoilandgas.sportbanter.online/',
      'https://unlimitedenergysystems.com/','https://atlxpres.com/')
def probe(url):
    started=time.monotonic()
    result=dict(url=url)
    try:
        with urllib.request.urlopen(urllib.request.Request(url,headers={'User-Agent':'ATL-Xpress-cohost-safety/1.0'}),timeout=2) as response:
            result['http_status']=response.status
            body=response.read(8192)
            if url.endswith('/ready'):result['ready']=json.loads(body).get('ok') is True
    except urllib.error.HTTPError as error:result.update(http_status=error.code,error_class='HTTPError')
    except Exception as error:result['error_class']=type(error).__name__
    result['seconds']=round(time.monotonic()-started,3)
    return result

report=dict(scope='read-only baseline failure diagnosis',services_changed=False,database_writes=False,load_started=False)
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:report['public_health']=list(pool.map(probe,URLS))
try:
    ids=subprocess.check_output(['docker','ps','-aq','--no-trunc'],text=True,timeout=10).split()
    rows=json.loads(subprocess.check_output(['docker','inspect',*ids],text=True,timeout=15)) if ids else []
    matching=[]
    for row in rows:
        labels=row.get('Config',{}).get('Labels',{}) or {}
        if any('samnadoilandgas.sportbanter.online' in str(v) for v in labels.values()):
            state=row.get('State',{})
            matching.append(dict(name=row['Name'],running=state.get('Running'),status=state.get('Status'),
                health=state.get('Health',{}).get('Status','not-configured'),restart_count=row.get('RestartCount'),
                oom_killed=state.get('OOMKilled'),exit_code=state.get('ExitCode')))
    report['affected_site_containers']=matching
    report['test_owned_container_count']=sum((r.get('Config',{}).get('Labels',{}) or {}).get('atlxpres.cohost.run')=='cohost-6d6c5f9220874022' for r in rows)
except Exception as error:report['inventory_error_class']=type(error).__name__
base=Path('/root/atlxpres-cohost/cohost-6d6c5f9220874022')
report['setup_state']={name:(base/name).exists() for name in ('images.tar.gz','manifest.json','local.lease','cleanup.complete')}
print(json.dumps(report,indent=2))
