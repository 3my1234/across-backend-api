"""Actual Docker/Compose rehearsal on a disposable GitHub runner only."""
import hashlib,json,os,shutil,signal,subprocess,sys,time,uuid
from pathlib import Path
from runtime import ROOT,call,compose,inventory,install_limits,exec_lab,sql,verify_limits,cleanup

def wait_ready(base):
    deadline=time.monotonic()+90
    while time.monotonic()<deadline:
        m,_=inventory(base)
        probe=subprocess.run(['docker','exec',m['services']['postgres'],'pg_isready','-U','lab','-d','capacity_lab'],capture_output=True,timeout=5)
        if probe.returncode==0:return
        time.sleep(1)
    raise RuntimeError('test PostgreSQL not ready')

def main():
    if os.environ.get('GITHUB_ACTIONS')!='true' or os.geteuid()!=0:raise ValueError('disposable CI root only')
    root=Path(__file__).resolve().parents[2]
    subprocess.run(['git','-c','safe.directory='+str(root),'diff','--exit-code','13d316c62ce8bdd35c1c2fe72bf3d4d357581914','HEAD','--','internal/','migrations/','cmd/api/','cmd/capacity-lab/'],check=True)
    run='cohost-'+uuid.uuid4().hex[:16];base=ROOT/run;base.mkdir(parents=True,mode=0o700)
    output=root/'singlehost-rehearsal-evidence';output.mkdir(exist_ok=False)
    result=dict(run=run,topology='singlehost',actor_pairs=500,scenario='large-busy',database_cpu=1,passed=False,capacity_qualified=False)
    sentinel=None;created=False
    try:
        tags=dict(app='atlxpres-singlehost-rehearsal:local',postgres='postgres:16-alpine',redis='redis:7.2-alpine',gateway='haproxy:3.0-alpine')
        images={k:call(['docker','image','inspect','--format','{{.Id}}',tag]).decode().strip() for k,tag in tags.items()}
        (base/'render.json').write_text(json.dumps(dict(run=run,images=images)))
        doc=json.loads(call(['node',str(root/'deploy/singlehost-lab/render.cjs'),str(base/'render.json')]))
        (base/'compose.runtime.json').write_text(json.dumps(doc,indent=2))
        (output/'compose.json').write_text(json.dumps(doc,indent=2))
        shutil.copyfile(root/'deploy/capacity-lab/haproxy.cfg',base/'haproxy.cfg')
        (base/'exchange').mkdir(mode=0o700)
        call(['docker','compose','-f',str(base/'compose.runtime.json'),'config','--quiet'])
        sentinel=call(['docker','run','-d','--network','none','--label','atlxpres.singlehost.sentinel='+run,images['redis'],'redis-server','--save','','--appendonly','no']).decode().strip()
        install_limits(run)
        # Mark cleanup necessary before create: a failed create can leave partial resources.
        created=True;compose(base,'create','--no-build','--pull','never')
        result['effective_cgroups']=verify_limits(base)
        compose(base,'start','postgres','redis','seed','exporter','verifier','lifecycle','queue-check')
        wait_ready(base)
        started=time.monotonic();exec_lab(base,'seed','seed',output/'seed.log',timeout=900)
        result['seed_seconds']=time.monotonic()-started
        counts=json.loads(sql(base,"SELECT json_build_object('actors',(SELECT count(*) FROM capacity_lab_actors),'migration108',(SELECT count(*) FROM schema_migrations WHERE name='108_push_receipt_claims.sql'),'identity',(SELECT marker FROM capacity_lab_identity))"))
        if counts!={'actors':500,'migration108':1,'identity':'disposable-compose-only'}:raise RuntimeError('actual seed schema/identity/population mismatch')
        result['seed_counts']=counts
        exec_lab(base,'exporter','prepare-sessions',output/'session-preparation.log')
        exec_lab(base,'exporter','notification-prepare',output/'notification-preparation.log')
        sql(base,'CREATE EXTENSION IF NOT EXISTS pg_stat_statements; ANALYZE;')
        compose(base,'start','api1','api2','gateway')
        deadline=time.monotonic()+90
        import urllib.request
        while True:
            try:
                with urllib.request.urlopen('http://127.0.0.1:58080/api/v1/ready',timeout=3) as response:
                    if json.load(response).get('ok') is not True:raise ValueError('unhealthy API')
                break
            except Exception:
                if time.monotonic()>deadline:raise
                time.sleep(1)
        exec_lab(base,'lifecycle','lifecycle',output/'lifecycle.jsonl')
        exec_lab(base,'queue-check','queue-check',output/'queue-check.jsonl')
        raw=exec_lab(base,'exporter','export');fixtures=json.loads(raw)
        if fixtures.get('origin')!='http://gateway:8080' or fixtures.get('marker')!='disposable-compose-only':raise ValueError('fixture origin/identity mismatch')
        exchange=base/'exchange';(exchange/'fixtures.json').write_bytes(raw)
        fixtures.pop('sessions');(exchange/'run.json').write_text(json.dumps(fixtures))
        sys.path.insert(0,str(root/'deploy/notification-lab'))
        from generator_prewarm import candidate,recover
        suite=base/'suite';suite.mkdir()
        for file in (root/'deploy/session-lab').glob('*.mjs'):shutil.copyfile(file,suite/file.name)
        original=(root/'deploy/session-lab/sessions.js').read_text();generated=candidate(original)
        if recover(generated)!=original or hashlib.sha256(generated.encode()).hexdigest()!='dad72e9b2b8dd7dd9fca94bb6a0d6856d4081a49d301b53e3e133c127d01ea49':raise ValueError('qualified generator source mismatch')
        (suite/'sessions.js').write_text(generated)
        # Rehearse the unchanged first20-session stage, not500 capacity.
        generator=['docker','run','--rm','--name',run+'-generator','--network','host','--add-host','gateway:127.0.0.1','--cpus','2','--memory','4g','--memory-swap','4g','--pids-limit','512','--user','0:0',
            '-v',str(suite)+':/suite:ro','-v',str(exchange)+':/exchange',
            '-e','K6_NO_USAGE_REPORT=true','-e','K6_NO_COLOR=true','-e','LAB_SESSION_PROFILE=smoke','-e','LAB_SESSION_TARGET=20','-e','LAB_SESSION_MIX=mixed','-e','LAB_SESSION_AUTH_MODE=established',
            'grafana/k6:2.3.0','run','--out','json=/exchange/timeseries.json','/suite/sessions.js']
        with (output/'k6.log').open('wb') as log:code=subprocess.run(generator,stdout=log,stderr=subprocess.STDOUT,timeout=330).returncode
        (exchange/'k6-exit-code.txt').write_text(str(code));shutil.copyfile(output/'k6.log',exchange/'k6.log')
        if (exchange/'summary.json').exists():shutil.copyfile(exchange/'summary.json',output/'summary.json')
        # Always attempt acknowledged-write reconciliation, including a failed load gate.
        exec_lab(base,'verifier','verify-sessions',output/'reconciliation.jsonl')
        if code:raise RuntimeError('unchanged20-session smoke gate failed: '+str(code))
        subprocess.run(['node',str(root/'deploy/cohost-lab/stage_gate.mjs'),str(output/'summary.json'),str(output/'reconciliation.jsonl'),'smoke','20'],check=True)
        result['smoke_sessions']=20;result['smoke_seconds']=45;result['write_reconciliation_passed']=True
        result['effective_cgroups_after_seed']=verify_limits(base)
        # Real safety.py invocation must stop owned services and leave the unrelated sentinel running.
        m,_=inventory(base);(base/'manifest.json').write_text(json.dumps(m));(base/'manifest.json').chmod(0o600)
        (base/'abort.json').write_text('{}')
        stopped=subprocess.run(['python3',str(root/'deploy/cohost-lab/safety.py'),str(base/'manifest.json'),str(base/'abort.json'),'--stop-test'],timeout=180)
        if stopped.returncode!=2:raise RuntimeError('safety fault did not fail closed')
        _,rows=inventory(base)
        if any(row['State']['Running'] for row in rows):raise RuntimeError('test services survived safety abort')
        if call(['docker','inspect','--format','{{.State.Running}}',sentinel]).decode().strip()!='true':raise RuntimeError('sentinel affected')
        result['owned_stop_and_sentinel_passed']=True;result['passed']=True
    finally:
        if created:
            try:
                m,_=inventory(base)
                for name,ident in m['services'].items():
                    with (output/(name+'.log')).open('wb') as log:subprocess.run(['docker','logs','--tail','100',ident],stdout=log,stderr=subprocess.STDOUT,timeout=15)
            finally:cleanup(base)
            result['cleanup_completed']=(base/'cleanup.complete').exists()
        if sentinel:
            if call(['docker','inspect','--format','{{index .Config.Labels "atlxpres.singlehost.sentinel"}}',sentinel]).decode().strip()!=run:raise RuntimeError('sentinel ownership mismatch')
            subprocess.run(['docker','rm','-f',sentinel],check=True,timeout=30,stdout=subprocess.DEVNULL)
        (output/'result.json').write_text(json.dumps(result,indent=2));print(json.dumps(result),flush=True)

if __name__=='__main__':main()
