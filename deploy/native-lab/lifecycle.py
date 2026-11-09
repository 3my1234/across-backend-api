"""No-load disposable Linux proof. Never runs on a VPS or contacts an API/DB.

Uses the launcher's real unit builder, a sleeping Docker container and a sentinel.
Proves systemd lifecycle behavior, not real two-host application readiness.
"""
import hashlib,json,os,signal,subprocess,sys,time,uuid
from pathlib import Path
from launch import commands
from native import atomic,SOURCE

LABEL='atlxpres.native.proof'
def call(args,**kwargs):return subprocess.check_output(args,timeout=30,**kwargs)
def wait(check,seconds=25):
    end=time.monotonic()+seconds
    while time.monotonic()<end:
        if check():return
        time.sleep(.2)
    raise RuntimeError('bounded no-load assertion timed out')
def exists(ident):return subprocess.run(['docker','inspect',ident],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0

def main():
    action=sys.argv[1]
    if os.name!='posix' or os.geteuid()!=0 or os.environ.get('GITHUB_ACTIONS')!='true':raise ValueError('disposable Linux CI only; no VPS load')
    if action in ('worker','peer'):
        base=Path(sys.argv[2])
        while True:
            atomic(base/(action+'.json'),dict(time=time.time(),pid=os.getpid()))
            time.sleep(.2)
    if action=='cleanup':
        base=Path(sys.argv[2]);run=sys.argv[3];ident=sys.argv[4]
        if exists(ident):
            row=json.loads(call(['docker','inspect',ident]))[0]
            if row['Config'].get('Labels',{}).get(LABEL)!=run:raise ValueError('foreign proof container')
            call(['docker','rm','-f',ident])
        atomic(base/'cleaned.json',dict(run=run,container_absent=not exists(ident)))
        return
    if action=='begin':
        base=Path(sys.argv[2]);run=sys.argv[3];ident=sys.argv[4];deadline=int(sys.argv[5])
        script=Path(__file__).resolve()
        prefix,peer,job=commands(script,base/'dummy.json',run,
            coordinator=[sys.executable,str(script),'worker',str(base)],
            cleanup=[sys.executable,str(script),'cleanup',str(base),run,ident],
            peer_task=[sys.executable,str(script),'peer',str(base)],lifetime=deadline)
        # The peer outlives the short coordinator deadline, so that case tests
        # the coordinator's RuntimeMaxSec rather than a peer stop.
        peer=[v if v!='--property=RuntimeMaxSec='+str(deadline) else '--property=RuntimeMaxSec=120' for v in peer]
        peer.insert(1,'--setenv=GITHUB_ACTIONS=true');job.insert(1,'--setenv=GITHUB_ACTIONS=true')
        call(peer);call(job)
        atomic(base/'units.json',dict(prefix=prefix))
        return  # Launching console/client process is now gone.
    if action!='prove':raise ValueError('unknown proof action')
    output=Path(sys.argv[2]).resolve();output.mkdir(parents=True,exist_ok=True)
    image=call(['docker','image','inspect','alpine:3.20','--format','{{.Id}}'],text=True).strip()
    sentinel=call(['docker','run','-d','--network','none','--memory','16m','--cpus','.1',image,'sleep','300'],text=True).strip()
    report=dict(passed=False,load_started=False,proof_scope='disposable single-host systemd and owned Docker cleanup; real two-host readiness is separate')
    units=[];containers=[]
    try:
        for case in ('term','kill','deadline','peer_loss'):
            run='cohost-'+uuid.uuid4().hex[:16];base=output/case;base.mkdir()
            ident=call(['docker','run','-d','--network','none','--memory','16m','--cpus','.1','--label',LABEL+'='+run,image,'sleep','120'],text=True).strip();containers.append(ident)
            call([sys.executable,str(Path(__file__).resolve()),'begin',str(base),run,ident,'4' if case=='deadline' else '60'])
            prefix=json.loads((base/'units.json').read_text())['prefix'];units.append(prefix)
            wait(lambda:(base/'peer.json').exists() and (base/'worker.json').exists())
            first=json.loads((base/'worker.json').read_text())['time'];time.sleep(.6)
            if json.loads((base/'worker.json').read_text())['time']<=first:raise RuntimeError('worker stopped with launcher')
            report['detached']=True
            if case=='term':call(['systemctl','stop',prefix+'-job'])
            elif case=='kill':call(['systemctl','kill','--kill-whom=main','--signal=SIGKILL',prefix+'-job'])
            elif case=='peer_loss':call(['systemctl','stop',prefix+'-peer'])
            wait(lambda:(base/'cleaned.json').exists() and not exists(ident))
            wait(lambda:subprocess.run(['systemctl','is-active','--quiet',prefix+'-peer']).returncode!=0)
            if not exists(sentinel):raise RuntimeError('unrelated sentinel was removed')
            report[{'term':'term_cleanup','kill':'kill_cleanup','deadline':'deadline_cleanup','peer_loss':'peer_loss_stops_job'}[case]]=True
            (base/'journal.txt').write_bytes(call(['journalctl','--no-pager','-u',prefix+'-job','-u',prefix+'-peer']))
        report.update(unrelated_container_preserved=True,passed=True)
    finally:
        for prefix in units:
            (output/(prefix+'-journal.txt')).write_bytes(call(['journalctl','--no-pager','-u',prefix+'-job','-u',prefix+'-peer']))
            subprocess.run(['systemctl','stop',prefix+'-job',prefix+'-peer'],check=False,timeout=40)
        for ident in containers+[sentinel]:
            if exists(ident):call(['docker','rm','-f',ident])
        names=['deploy/native-lab/'+p.name for p in Path(__file__).parent.glob('*.py') if not p.name.startswith('test_')]
        names+=['deploy/native-lab/render.cjs','deploy/singlehost-lab/message_fanout_replay.py']
        names+=['deploy/cohost-lab/'+p.name for p in (SOURCE/'deploy/cohost-lab').glob('*.py') if not p.name.startswith('test_')]
        report['code_hashes']={n:hashlib.sha256((SOURCE/n).read_bytes()).hexdigest() for n in sorted(names)}
        atomic(output/'native-lifecycle.json',report)
    print(json.dumps(report,indent=2))
if __name__=='__main__':main()
