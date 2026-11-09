"""Linux-only entry points. Start is manual; prepare-only never runs k6."""
import argparse,base64,datetime,gzip,hashlib,json,os,signal,subprocess,sys,time,uuid
from pathlib import Path
from native import atomic,load,execute,cleanup,stop_generator,peer,HERE,SOURCE
from setup import ROOT,catalog,base_for,initialize,prepare_main,prepare_database,phase,call,manager
from launch import commands
from native_budget import setup as budget_setup

def active():
    data=json.loads((ROOT/'active.json').read_text())
    base=base_for(data['run'])
    return json.loads((base/'native.json').read_text()),base
def cleanup_active(config):
    base=Path(config['main_base']);(base/'native-draining').touch()
    # Manager can clean an interrupted setup even before per-run keys exist.
    warning=stop_generator(config)
    remote_error=None
    try:
        result=json.loads(manager(config,dict(action='cleanup',run=base.name),timeout=120))
        if result.get('cleanup_confirmed') is not True:raise RuntimeError('database did not confirm cleanup')
    except Exception as error:remote_error=type(error).__name__
    # Existing cleanup owns immutable IDs, volumes and keys. Stop the generator
    # first through the native ownership check; remote cleanup is idempotent.
    cleanup(config,database_confirmed=remote_error is None)
    if remote_error:raise RuntimeError('database manager cleanup unconfirmed: '+remote_error)
    if warning:raise RuntimeError(warning)
    prefix='atl-native-'+base.name.removeprefix('cohost-')
    subprocess.run(['systemctl','--no-block','stop',prefix+'-peer'],check=False,timeout=10)
def start(live):
    c=catalog()
    if c['role']!='main':raise ValueError('start on main VPS only')
    if live and not __import__('re').fullmatch('sha256:[a-f0-9]{64}',c.get('generator_image') or ''):raise ValueError('generator cache not prepared; no download during live execution')
    if (ROOT/'active.json').exists():
        _,old=active()
        if not (old/'cleanup.complete').exists():raise ValueError('previous run not confirmed cleaned; inspect status before another start')
        state=subprocess.run(['systemctl','show','atl-native-'+old.name.removeprefix('cohost-')+'-job','--property=ActiveState','--value'],capture_output=True,text=True,timeout=10).stdout.strip()
        if state not in ('','inactive','failed'):raise ValueError('previous coordinator still stopping; do not duplicate')
    expires=datetime.datetime.fromisoformat(c['manager_expires_utc']).timestamp()
    if expires-time.time()<7500:raise ValueError('restricted inter-host setup key expires before full hard deadline')
    proof=json.loads((ROOT/'native-lifecycle.json').read_text())
    if proof.get('passed') is not True or proof.get('shared_budget_verified') is not True:raise ValueError('passed Linux proof required')
    for name,digest in proof['code_hashes'].items():
        if hashlib.sha256((SOURCE/name).read_bytes()).hexdigest()!=digest:raise ValueError('prepared runner differs from proof')
    run='cohost-'+uuid.uuid4().hex[:16]
    expiry=time.strftime('%Y%m%d%H%M%SZ',time.gmtime(time.time()+10800))
    keys={};private={}
    # Generate native inter-host keys locally; no general root credential leaves Windows.
    keydir=ROOT/('keys-'+run);keydir.mkdir(mode=0o700)
    for kind in ('ci-main','ci-database','main-database'):
        path=keydir/kind
        call(['ssh-keygen','-q','-t','ed25519','-N','','-C',run+'-'+kind,'-f',str(path)])
        keys[kind]=path.with_suffix('.pub').read_text().strip();private[kind]=path
    base=initialize(c,run,keys,live,expiry)
    for kind,path in private.items():
        target=base/(kind+'.key');target.write_bytes(path.read_bytes());target.chmod(0o600)
        path.unlink();path.with_suffix('.pub').unlink()
    keydir.rmdir()
    (base/'database_known_hosts').write_text(c['database_known_hosts'])
    from install_ci_bundle import socket_directory,control_path
    sockets=socket_directory(base)
    peerconf='\n'.join(['Host cohost-database',' HostName 79.143.176.173',' User root',' BatchMode yes',' IdentitiesOnly yes',' StrictHostKeyChecking yes',' IdentityFile '+str(base/'ci-database.key'),' UserKnownHostsFile '+str(base/'database_known_hosts'),' ControlMaster auto',' ControlPath '+control_path(sockets),' ControlPersist 10',' ConnectTimeout 5',' ServerAliveInterval 5',' ServerAliveCountMax 1'])+'\n'
    (base/'peer.conf').write_text(peerconf);(base/'peer.conf').chmod(0o600)
    (base/'ssh_config').write_text(peerconf);(base/'ssh_config').chmod(0o600)
    payload=dict(action='prepare',run=run,keys=keys,live=live,expiry=expiry)
    atomic(base/'database-payload.json',payload)
    config=dict(run=run,main_base=str(base),peer_config=str(base/'peer.conf'),manager_config=str(ROOT/'manager.conf'),generator_image=c['generator_image'],generator_ip='172.29.248.21',network=run+'-main_lab',stages=[list(s) for s in __import__('trial_policy').STAGES],application=c['application'],client_hashes=__import__('trial_policy').client_hashes(SOURCE),code_hashes=proof['code_hashes'],lifecycle_sha256=hashlib.sha256((ROOT/'native-lifecycle.json').read_bytes()).hexdigest(),live_authorized=live)
    atomic(base/'native.json',config);(base/'native-lifecycle.json').write_bytes((ROOT/'native-lifecycle.json').read_bytes())
    atomic(ROOT/'active.json',dict(run=run,load_authorized=live))
    for command in budget_setup(run):call(command,timeout=30)
    prefix,p,j=commands(HERE/'native.py',base/'native.json',run,coordinator=[sys.executable,str(HERE/'entry.py'),'worker'],cleanup=[sys.executable,str(HERE/'entry.py'),'cleanup'],peer_task=[sys.executable,str(HERE/'entry.py'),'peer'])
    try:call(p);call(j)
    except BaseException:cleanup_active(config);raise
    print(json.dumps(dict(run=run,unit=prefix+'-job',detached=True,load_authorized=live,load_started=False,hard_limit_seconds=7200)))
def database_access():
    c=catalog()
    if c['role']!='database':raise ValueError('database manager endpoint only')
    command=os.environ.get('SSH_ORIGINAL_COMMAND','')
    if command=='native-verifier':
        data=Path(c['verifier']).read_bytes()
        if hashlib.sha256(data).hexdigest()!=c['verifier_sha256']:raise ValueError('cached verifier mismatch')
        sys.stdout.buffer.write(gzip.compress(data));return
    if not command.startswith('native-setup ') or len(command)>16384:raise ValueError('fixed native manager command required')
    op=json.loads(base64.b64decode(command[13:],validate=True));base=base_for(op['run']);action=op.get('action')
    if action=='prepare':
        initialize(c,op['run'],op['keys'],op['live'],op['expiry'])
        unit=op['run'].replace('-','')+'nativeprepare'
        call(['systemd-run','--quiet','--unit',unit,'--property=Type=exec','--property=RuntimeMaxSec=1200','--property=CPUQuota=25%','--property=MemoryMax=256M','--property=KillMode=control-group',sys.executable,str(HERE/'entry.py'),'database-worker',str(base)])
        print(json.dumps(dict(prepare_started=True,load_started=False)));return
    if base.exists():
        origin=base/'native-origin.json'
        if not origin.is_file() or json.loads(origin.read_text()).get('created_by')!='native-manager':raise ValueError('not a run created by this native manager')
    if action=='status':
        path=base/'setup-progress.json'
        print(path.read_text() if path.exists() else json.dumps(dict(ready=False,phase='not started')));return
    if action=='cleanup':
        if base.exists():
            subprocess.run(['systemctl','stop',base.name.replace('-','')+'nativeprepare'],check=False,timeout=30)
            sys.path.insert(0,str(base/'tools'));from operator_setup import clean
            clean(base)
        print(json.dumps(dict(cleanup_confirmed=True,run=base.name)));return
    raise ValueError('native manager action denied')
def main():
    if os.name!='posix' or os.geteuid()!=0:raise ValueError('Linux root required')
    p=argparse.ArgumentParser();p.add_argument('action',choices=('start','status','worker','peer','cleanup','database-access','database-worker'));p.add_argument('base',nargs='?');p.add_argument('--approve-trial',action='store_true');p.add_argument('--prepare-only',action='store_true');a=p.parse_args()
    if a.action=='database-access':database_access();return
    if a.action=='database-worker':
        base=base_for(Path(a.base).name)
        try:prepare_database(base,catalog())
        except BaseException as error:phase(base,'failed',failed=True,error=type(error).__name__+': '+str(error));raise
        return
    if a.action=='start':
        if a.approve_trial==a.prepare_only:raise ValueError('choose explicit --approve-trial OR no-load --prepare-only')
        import fcntl
        with (ROOT/'start.lock').open('w') as lock:
            fcntl.flock(lock,fcntl.LOCK_EX);start(a.approve_trial)
        return
    config,base=active()
    if a.action=='status':
        for name in ('setup-progress.json','database-setup-progress.json','progress.json','result.json','native-cleanup.json'):
            path=base/name
            if path.exists():print(name+': '+path.read_text())
        print(call(['systemctl','show','atl-native-'+base.name.removeprefix('cohost-')+'-job','--property=ActiveState,SubState,Result,ExecMainStatus']).decode());return
    if a.action=='cleanup':cleanup_active(config);return
    if a.action=='peer':
        deadline=time.monotonic()+1200
        while not (base/'setup.complete').exists():
            if (base/'native-draining').exists():time.sleep(.5);continue
            if time.monotonic()>deadline:raise RuntimeError('bounded native setup deadline')
            time.sleep(.5)
        peer(load(base/'native.json'));return
    try:
        prepare_main(config)
        config=load(base/'native.json')
        if config['live_authorized']:execute(config)
        else:
            phase(base,'no-load runtime ready; awaiting rehearsal stop',ready=True)
            while True:time.sleep(10)
    except BaseException as error:
        phase(base,'failed',failed=True,error=type(error).__name__+': '+str(error));raise
if __name__=='__main__':main()
