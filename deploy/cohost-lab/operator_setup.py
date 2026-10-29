"""Setup-only operator orchestration; never seeds or starts application traffic."""
import argparse,json,os,re,subprocess,time
from pathlib import Path

def call(args, **kwargs):
    return subprocess.run(args,check=True,timeout=180,**kwargs)

def clean(base):
    import fcntl
    with (base/'cleanup.lock').open('w') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        if (base/'cleanup.complete').exists():return
        request=json.loads((base/'request.json').read_text())
        prefix=request['run'].replace('-',''); role=request['role']
        # Stop only exact nonce-named units created by this package.
        for suffix in (role+'guard.service','dbtunnel.service'):
            subprocess.run(['systemctl','stop',prefix+suffix],check=False,timeout=30)
        manifest=base/'manifest.json'
        if manifest.exists() and json.loads(manifest.read_text()).get('container_ids'):
            call(['python3',str(base/'tools/cleanup.py'),str(manifest),'--execute','--remove-disposable-data'])
        call(['python3',str(base/'tools/install_keys.py'),str(base),'--execute','--remove'])
        subprocess.run(['systemctl','stop',prefix+role+'anchor.service'],check=False,timeout=30)
        (base/'main-database.key').unlink(missing_ok=True)
        (base/'cleanup.complete').write_text(str(time.time()))

def main():
    p=argparse.ArgumentParser();p.add_argument('directory',type=Path)
    p.add_argument('action',choices=['baseline','stage','verify','reaper','cleanup']);a=p.parse_args()
    base=a.directory.resolve();r=json.loads((base/'request.json').read_text());run=r['run'];role=r['role']
    if os.geteuid()!=0 or not re.fullmatch('cohost-[a-f0-9]{16}',run) or base!=Path('/root/atlxpres-cohost')/run:
        raise ValueError('fixed protected run required')
    approval=json.loads((base/'approval.json').read_text())
    if approval.get('run')!=run or approval.get('authorized_setup') is not True or approval.get('load_authorized') is not False:
        raise ValueError('setup-only approval required')
    prefix=run.replace('-','')
    if a.action=='cleanup':clean(base);return
    if a.action=='baseline':
        subprocess.run(['python3',str(base/'tools/baseline.py'),str(base/'baseline.json')],check=True,timeout=420);return
    if a.action=='reaper':
        # Independent of the test slice and operator SSH session. Cleanup on loss
        # of the tested guardian or after fourteen minutes, even on success.
        start=time.monotonic()
        while time.monotonic()-start<840:
            if (base/'cleanup.complete').exists():return
            lease=base/'local.lease'
            if time.monotonic()-start>30 and (not lease.exists() or time.time()-lease.stat().st_mtime>10):break
            time.sleep(2)
        clean(base);return
    import fcntl
    try:
        with (base/'cleanup.lock').open('w') as operation_lock:
            fcntl.flock(operation_lock,fcntl.LOCK_EX)
            if (base/'cleanup.complete').exists():raise ValueError('run already cleaned; no restart')
            if a.action=='stage':
                (base/'tunnel').mkdir(mode=0o700,exist_ok=True)
                (base/'exchange').mkdir(mode=0o700,exist_ok=True)
                call(['systemd-run','--unit',prefix+role+'reaper.service','--property','MemoryMax=128M','--property','CPUQuota=25%',
                      '/usr/bin/python3',str(base/'tools/operator_setup.py'),str(base),'reaper'])
                call(['python3',str(base/'tools/stage_host.py'),str(base),'--execute'])
                call(['python3',str(base/'tools/install_keys.py'),str(base),'--execute'])
                call(['python3',str(base/'tools/verify_cgroup.py'),str(base/'manifest.json')])
            elif a.action=='verify':
                if role!='main':raise ValueError('main verification only')
                key=base/'main-database.key';key.chmod(0o600)
                call(['systemd-run','--unit',prefix+'dbtunnel.service','--slice',prefix+'main.slice',
                      '--property','CPUQuota=25%','--property','MemoryMax=64M',
                      '/usr/bin/ssh','-i',str(key),'-o','BatchMode=yes','-o','StrictHostKeyChecking=yes',
                      '-o','UserKnownHostsFile='+str(base/'database_known_hosts'),'-o','ExitOnForwardFailure=yes',
                      '-o','ServerAliveInterval=5','-o','ServerAliveCountMax=2','-N',
                      '-L',str(base/'tunnel/.s.PGSQL.55432')+':127.0.0.1:55432','root@79.143.176.173'])
                for _ in range(50):
                    if (base/'tunnel/.s.PGSQL.55432').exists():break
                    time.sleep(.2)
                else:raise ValueError('test tunnel did not open')
                call(['python3',str(base/'tools/verify_transport.py'),str(base/'manifest.json')])
                (base/'verification.complete').write_text(json.dumps(dict(run=run,transport='passed',load_started=False)))
    except BaseException:
        clean(base)
        raise

if __name__=='__main__':main()
