"""Replay the actual synthetic seed in disposable CI, never on live hosts.

Isolates seed correctness/resource failures from the cross-host lease relay.
No API traffic, production URLs, provider transports or capacity claim.
"""
import json,os,subprocess,time,uuid
from pathlib import Path

SEED_VERIFICATION_SQL="SELECT json_build_object('actors',(SELECT count(*) FROM capacity_lab_actors),'migration108',(SELECT count(*) FROM schema_migrations WHERE name='108_push_receipt_claims.sql'),'identity',(SELECT marker FROM capacity_lab_identity))"

def call(*args,timeout=30):
    return subprocess.check_output(args,text=True,timeout=timeout).strip()

def main():
    if os.environ.get('GITHUB_ACTIONS')!='true':raise ValueError('disposable CI only')
    root=Path(__file__).resolve().parents[2]
    source=call('git','-c','safe.directory='+str(root),'rev-parse','HEAD')
    subprocess.run(['git','-c','safe.directory='+str(root),'diff','--exit-code','13d316c62ce8bdd35c1c2fe72bf3d4d357581914','HEAD','--','internal/','migrations/','cmd/api/','cmd/capacity-lab/'],check=True)
    run='cohost-'+uuid.uuid4().hex[:16]
    pg=seed=network=None
    output=root/'seed-replay-evidence';output.mkdir(exist_ok=False)
    result=dict(source=source,application_image_source='13d316c62ce8bdd35c1c2fe72bf3d4d357581914',database_cpus=3,database_memory_bytes=3*2**30,seed_cpus=1,seed_memory_bytes=512*2**20,actor_pairs=500,scenario='large-busy',application_load_started=False,passed=False)
    try:
        network=call('docker','network','create','--internal','--label','atlxpres.seed-replay='+run,run)
        pg=call('docker','run','-d','--network',network,'--network-alias','postgres','--label','atlxpres.seed-replay='+run,'--label','atlxpres.cohost.run='+run,'--cpus','3','--memory','3g','--memory-swap','3g','-e','POSTGRES_USER=lab','-e','POSTGRES_PASSWORD=lab-disposable-only','-e','POSTGRES_DB=capacity_lab','postgres:16-alpine')
        ready=time.monotonic()+60
        while True:
            probe=subprocess.run(['docker','exec',pg,'pg_isready','-U','lab','-d','capacity_lab'],capture_output=True,timeout=5)
            if probe.returncode==0:break
            if time.monotonic()>ready:raise RuntimeError('disposable database startup failed')
            time.sleep(.2)
        seed=call('docker','create','--network',network,'--label','atlxpres.seed-replay='+run,'--label','atlxpres.cohost.run='+run,'--cpus','1','--memory','512m','--memory-swap','512m','-e','CAPACITY_LAB=disposable-compose-only','-e','LAB_SCENARIO=large-busy','-e','LAB_ACTOR_PAIRS=500','-e','LAB_DB_CPUS=3','-e','LAB_EXPANDED_JOURNEYS=1','atlxpres-seed-replay:local','/bin/sleep','infinity')
        started=time.monotonic()
        call('docker','start',seed)
        from ci_peer_replay import PeerReplay
        with PeerReplay(root,run,pg,seed,output) as peer:
            # Actual seed command retains its12minute context deadline. Real
            # multiplexed SSH must continue refreshing leases throughout it.
            peer.seed_database()
            result.update(seed_seconds=time.monotonic()-started,seed_exit_code=0)
            states=json.loads(call('docker','inspect',pg,seed))
            result['states']=[dict(role=role,running=s['State']['Running'],oom=s['State']['OOMKilled'],exit_code=s['State']['ExitCode']) for role,s in zip(('database','seed'),states)]
            if any(s['State']['OOMKilled'] for s in states):raise RuntimeError('seed OOM')
            counts=json.loads(call('docker','exec',pg,'psql','-U','lab','-d','capacity_lab','-XAtc',SEED_VERIFICATION_SQL))
            result['counts']=counts
            if counts!={'actors':500,'migration108':1,'identity':'disposable-compose-only'}:raise RuntimeError('seed completion identity/count mismatch')
            peer.verify_fault_stop()
        if call('docker','inspect','--format','{{.State.Running}}',pg)!='false':raise RuntimeError('fault did not stop isolated database')
        result['real_ssh_seed_and_fault_replay_passed']=True
        result['passed']=True
    finally:
        cleanup_errors=[]
        for name,ident in (('seed',seed),('postgres',pg)):
            if ident:
                try:
                    with (output/(name+'.log')).open('w') as out:subprocess.run(['docker','logs',ident],stdout=out,stderr=subprocess.STDOUT,timeout=20)
                except Exception as error:cleanup_errors.append(name+' log: '+str(error))
                try:
                    label=call('docker','inspect','--format','{{index .Config.Labels "atlxpres.seed-replay"}}',ident)
                    if label!=run:raise ValueError('cleanup ownership mismatch')
                    subprocess.run(['docker','rm','-f','-v',ident],check=True,timeout=30)
                except Exception as error:cleanup_errors.append(name+' remove: '+str(error))
        if network:
            try:subprocess.run(['docker','network','rm',network],check=True,timeout=30)
            except Exception as error:cleanup_errors.append('network remove: '+str(error))
        result['cleanup_completed']=not cleanup_errors
        result['cleanup_errors']=cleanup_errors
        (output/'result.json').write_text(json.dumps(result,indent=2))
        print(json.dumps(result),flush=True)
        if cleanup_errors:raise RuntimeError('disposable cleanup incomplete')

if __name__=='__main__':main()
