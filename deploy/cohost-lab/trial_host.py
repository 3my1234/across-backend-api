"""Activate ONLY a separately approved already-isolated trial; never stage production."""
import json,os,subprocess,time
from pathlib import Path
from trial_policy import validate,LIFETIME

def activate(base):
    from baseline import validate as baseline
    from safety import validate_inventory
    approval=json.loads((base/'trial-approval.json').read_text());m=json.loads((base/'manifest.json').read_text())
    validate(approval,m['run'])
    if (base/'cleanup.complete').exists() or (base/'trial-started').exists():raise ValueError('closed/already active trial')
    if base!=Path('/root/atlxpres-cohost')/m['run']:raise ValueError('protected trial root required')
    for path in (base,base/'trial-approval.json',base/'manifest.json'):
        st=path.stat()
        if st.st_uid!=0 or st.st_mode&0o022:raise ValueError('unsafe activation ownership')
    baseline(json.loads((base/'baseline.json').read_text()),time.time())
    from datetime import datetime,timezone
    request=json.loads((base/'request.json').read_text())
    expiry=datetime.strptime(request['key_expiry_utc'],'%Y%m%d%H%M%SZ').replace(tzinfo=timezone.utc).timestamp()
    if expiry-time.time()<LIFETIME+300:raise ValueError('restricted keys expire before bounded trial')
    if not (base/'peer.lease').exists() or time.time()-(base/'peer.lease').stat().st_mtime>5:raise ValueError('fresh peer lease required')
    rows=json.loads(subprocess.check_output(['docker','inspect',*m['container_ids']],text=True,timeout=10))
    validate_inventory(m,rows)
    def call(args):subprocess.run(args,check=True,timeout=30)
    prefix=m['run'].replace('-','');role=m['role']
    # Reaper remains outside test slice, independent of CI or SSH lifetime.
    call(['systemctl','stop',prefix+role+'reaper.service'])
    call(['systemd-run','--unit',prefix+role+'trialreaper.service','--property','MemoryMax=128M','--property','CPUQuota=25%',
          '/usr/bin/python3',str(base/'tools/trial_host.py'),str(base),'reap'])
    call(['systemctl','stop',prefix+role+'guard.service'])
    m['load_authorized']=True;m['fixture_preparation_authorized']=True
    m['trial_deadline_unix']=time.time()+LIFETIME
    (base/'manifest.json').write_text(json.dumps(m))
    (base/'trial-started').write_text(str(time.time()))
    if role=='database':
        call(['systemctl','set-property','--runtime',prefix+'database.slice','CPUQuota=300%'])
        call(['docker','update','--cpus','3',m['services']['postgres']])
    call(['systemd-run','--unit',prefix+role+'guard.service','--property','MemoryMax=128M','--property','CPUQuota=25%',
          '/usr/bin/python3',str(base/'tools/watchdog.py'),str(base/'manifest.json'),'--stop-test'])
    call(['python3',str(base/'tools/verify_cgroup.py'),str(base/'manifest.json')])
    print(json.dumps(dict(activated=role,run=m['run'],application_load_started=False)),flush=True)

def operation(base,action):
    from control import command
    from safety import validate_inventory
    m=json.loads((base/'manifest.json').read_text())
    validate(json.loads((base/'trial-approval.json').read_text()),m['run'])
    if m.get('load_authorized') is not True or time.time()>=m.get('trial_deadline_unix',0):raise ValueError('live trial approval required')
    rows=json.loads(subprocess.check_output(['docker','inspect',*m['container_ids']],text=True,timeout=10))
    validate_inventory(m,rows)
    if action=='publish-fixture':
        raw=subprocess.check_output(command(m,dict(action='exec',service='exporter',args=['/app/lab','export'])),timeout=120)
        data=json.loads(raw)
        if data.get('marker')!='disposable-compose-only' or data.get('origin')!='http://gateway:8080':raise ValueError('disposable fixture required')
        (base/'exchange/fixtures.json').write_bytes(raw)
        data.pop('sessions',None)
        (base/'exchange/run.json').write_text(json.dumps(data))
        print('Synthetic fixture published; tokens withheld',flush=True)
    elif action=='gateway-relay':
        prefix=m['run'].replace('-','')
        subprocess.run(['systemd-run','--unit',prefix+'mainrelay.service','--slice',prefix+'main.slice',
            '--property','CPUQuota=25%','--property','MemoryMax=64M','--property','TasksMax=128',
            '/usr/bin/python3',str(base/'tools/host_relay.py'),str(base/'manifest.json')],check=True,timeout=30)
    elif action in ('observations','observations-full'):
        names=('sample.json','watchdog.jsonl','verification.complete','image-identities.json')
        report={name:(base/name).read_text() for name in names if (base/name).is_file()}
        if action=='observations' and 'watchdog.jsonl' in report:report['watchdog.jsonl']='\n'.join(report['watchdog.jsonl'].splitlines()[-20:])
        report['containers']=subprocess.check_output(['docker','stats','--no-stream','--format','{{json .}}',*m['container_ids']],text=True,timeout=15)
        parent=Path('/sys/fs/cgroup')/(m['run'].replace('-','')+m['role']+'.slice')
        report['parent_cgroup']={name:(parent/name).read_text() for name in ('cpu.max','cpu.stat','memory.max','memory.current','memory.events','io.stat')}
        print(json.dumps(report))
    else:raise ValueError('trial operation denied')

def reap(base):
    from operator_setup import clean
    start=time.monotonic()
    while time.monotonic()-start<LIFETIME:
        if (base/'cleanup.complete').exists():return
        lease=base/'local.lease'
        if time.monotonic()-start>30 and (not lease.exists() or time.time()-lease.stat().st_mtime>10):break
        time.sleep(2)
    clean(base)

if __name__=='__main__':
    import sys
    base=Path(sys.argv[1]).resolve()
    if os.geteuid()!=0:raise ValueError('Linux root required')
    if sys.argv[2]=='reap':reap(base)
    else:raise ValueError('activation through approved restricted endpoint only')
