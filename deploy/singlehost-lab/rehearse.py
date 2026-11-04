"""Actual Docker/Compose rehearsal on a disposable GitHub runner only."""
import hashlib,json,os,shutil,signal,subprocess,sys,time,uuid
from pathlib import Path
from runtime import ROOT,call,compose,inventory,install_limits,exec_lab,sql,verify_limits,verify_host_bridge,cleanup

def wait_ready(base):
    deadline=time.monotonic()+90
    while time.monotonic()<deadline:
        m,_=inventory(base)
        probe=subprocess.run(['docker','exec',m['services']['postgres'],'psql','-h','127.0.0.1','-U','lab','-d','capacity_lab','-XAt','-v','ON_ERROR_STOP=1','-c','SELECT 1'],capture_output=True,timeout=5)
        if probe.returncode==0 and probe.stdout.strip()==b'1':return
        time.sleep(1)
    raise RuntimeError('test PostgreSQL not ready')

def small_queue_preflight(root,images,output):
    """Catch queue fixture/lifecycle errors before the expensive500-pair seed."""
    run='cohost-'+uuid.uuid4().hex[:16];base=ROOT/run;base.mkdir(parents=True,mode=0o700)
    (base/'render.json').write_text(json.dumps(dict(run=run,images=images)))
    doc=json.loads(call(['node',str(root/'deploy/singlehost-lab/render.cjs'),str(base/'render.json')]))
    doc['services']={key:value for key,value in doc['services'].items() if key in ('postgres','redis','seed','exporter','queue-check')}
    for service in doc['services'].values():
        env=service.get('environment',{})
        if 'LAB_SCENARIO' in env:env['LAB_SCENARIO']='busy'
        if 'LAB_ACTOR_PAIRS' in env:env['LAB_ACTOR_PAIRS']='10'
    (base/'exchange').mkdir();(base/'compose.runtime.json').write_text(json.dumps(doc))
    try:
        install_limits(run);compose(base,'create','--no-build','--pull','never')
        compose(base,'start',*doc['services'])
        wait_ready(base);verify_host_bridge(base)
        exec_lab(base,'seed','seed',output/'small-seed.log',timeout=120)
        exec_lab(base,'exporter','prepare-sessions')
        from queue_probe import exercise as queue_probe
        queue_probe(lambda text:sql(base,text),lambda:exec_lab(base,'queue-check','queue-check',output/'small-queue-check.jsonl'))
        print('Small real queue fixture/retry/recovery/cleanup preflight passed',flush=True)
    finally:cleanup(base)

def main():
    if os.environ.get('GITHUB_ACTIONS')!='true' or os.geteuid()!=0:raise ValueError('disposable CI root only')
    import argparse
    parser=argparse.ArgumentParser();parser.add_argument('--foundation',action='store_true');args=parser.parse_args()
    pairs=10 if args.foundation else 500
    root=Path(__file__).resolve().parents[2]
    from provenance import verify
    verify(root)
    from check_seed import check
    retained=check()
    run='cohost-'+uuid.uuid4().hex[:16];base=ROOT/run;base.mkdir(parents=True,mode=0o700)
    output=root/'singlehost-rehearsal-evidence';output.mkdir(exist_ok=False)
    result=dict(run=run,topology='singlehost',actor_pairs=pairs,scenario='large-busy',database_cpu=1,passed=False,capacity_qualified=False,retained_full_seed=retained)
    sentinel=None;created=False
    try:
        tags=dict(app='atlxpres-singlehost-rehearsal:local',postgres='postgres:16-alpine',redis='redis:7.2-alpine',gateway='haproxy:3.0-alpine')
        images={k:call(['docker','image','inspect','--format','{{.Id}}',tag]).decode().strip() for k,tag in tags.items()}
        if not args.foundation:
            small_queue_preflight(root,images,output)
            result['small_queue_preflight_passed']=True
        (base/'render.json').write_text(json.dumps(dict(run=run,images=images,verifier_override=True)))
        with (base/'verifier.bin').open('wb') as binary:subprocess.run(['docker','run','--rm','--network','none','--entrypoint','/bin/cat',images['app'],'/app/lab'],stdout=binary,check=True,timeout=30)
        (base/'verifier.bin').chmod(0o500)
        result['verifier_sha256']=hashlib.sha256((base/'verifier.bin').read_bytes()).hexdigest()
        doc=json.loads(call(['node',str(root/'deploy/singlehost-lab/render.cjs'),str(base/'render.json')]))
        if args.foundation:
            for service in doc['services'].values():
                if 'LAB_ACTOR_PAIRS' in service.get('environment',{}):service['environment']['LAB_ACTOR_PAIRS']='10'
        (base/'compose.runtime.json').write_text(json.dumps(doc,indent=2))
        (base/'compose.json').write_text(json.dumps(doc,indent=2))
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
        result.update(verify_host_bridge(base))
        print('Actual TCP PostgreSQL ready; starting '+str(pairs)+'-pair disposable rehearsal seed',flush=True)
        started=time.monotonic();exec_lab(base,'seed','seed',output/'seed.log',timeout=900)
        result['seed_seconds']=time.monotonic()-started
        counts=json.loads(sql(base,"SELECT json_build_object('actors',(SELECT count(*) FROM capacity_lab_actors),'migration108',(SELECT count(*) FROM schema_migrations WHERE name='108_push_receipt_claims.sql'),'identity',(SELECT marker FROM capacity_lab_identity))"))
        if counts!={'actors':pairs,'migration108':1,'identity':'disposable-compose-only'}:raise RuntimeError('actual seed schema/identity/population mismatch')
        result['seed_counts']=counts
        print('Seed counts/schema verified; preparing private synthetic sessions',flush=True)
        # This command emits bearer fixtures. Capture privately, never in public evidence.
        exec_lab(base,'exporter','prepare-sessions')
        exec_lab(base,'exporter','notification-prepare',output/'notification-preparation.log')
        sql(base,'CREATE EXTENSION IF NOT EXISTS pg_stat_statements; ANALYZE;')
        compose(base,'start','api1','api2','gateway')
        deadline=time.monotonic()+90
        import urllib.request
        opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
        while True:
            try:
                with opener.open('http://172.29.248.10:8080/api/v1/ready',timeout=3) as response:
                    if json.load(response).get('ok') is not True:raise ValueError('unhealthy API')
                break
            except Exception:
                if time.monotonic()>deadline:raise
                time.sleep(1)
        exec_lab(base,'lifecycle','lifecycle',output/'lifecycle.jsonl')
        from queue_probe import exercise as queue_probe
        queue_probe(lambda text:sql(base,text),lambda:exec_lab(base,'queue-check','queue-check',output/'queue-check.jsonl'))
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
        generator=['docker','run','--rm','--name',run+'-generator','--network','host','--add-host','gateway:172.29.248.10','--cpus','2','--memory','4g','--memory-swap','4g','--pids-limit','512','--user','0:0',
            '-v',str(suite)+':/suite:ro','-v',str(exchange)+':/exchange',
            '-e','K6_NO_USAGE_REPORT=true','-e','K6_NO_COLOR=true','-e','LAB_SESSION_PROFILE=smoke','-e','LAB_SESSION_TARGET=20','-e','LAB_SESSION_MIX=mixed','-e','LAB_SESSION_AUTH_MODE=established',
            'grafana/k6:2.3.0','run','--out','json=/exchange/timeseries.json','/suite/sessions.js']
        print('Starting actual unchanged20-session smoke',flush=True)
        with (output/'k6.log').open('wb') as log:code=subprocess.run(generator,stdout=log,stderr=subprocess.STDOUT,timeout=330).returncode
        (exchange/'k6-exit-code.txt').write_text(str(code));shutil.copyfile(output/'k6.log',exchange/'k6.log')
        for filename in ('summary.json','timeseries.json'):
            if (exchange/filename).exists():shutil.copyfile(exchange/filename,output/filename)
        # Always attempt acknowledged-write reconciliation, including a failed load gate.
        application_failures=[]
        try:exec_lab(base,'verifier','verify-sessions',output/'reconciliation.jsonl')
        except Exception as error:application_failures.append('reconciliation: '+str(error))
        if code:application_failures.append('unchanged20-session k6 gate exit '+str(code))
        if not application_failures:
            gate=subprocess.run(['node',str(root/'deploy/cohost-lab/stage_gate.mjs'),str(output/'summary.json'),str(output/'reconciliation.jsonl'),'smoke','20'])
            if gate.returncode:application_failures.append('original stage gate failed')
        result['planned_smoke_sessions']=20;result['planned_smoke_seconds']=45;result['application_failures']=application_failures
        result['write_reconciliation_passed']=not application_failures
        result['effective_cgroups_after_seed']=verify_limits(base)
        # Preserve logs before the actual hard-stop integration destroys the lab.
        m,_=inventory(base)
        for name,ident in m['services'].items():
            with (output/(name+'-container.log')).open('wb') as log:subprocess.run(['docker','logs','--tail','2000',ident],stdout=log,stderr=subprocess.STDOUT,timeout=15)
        from rehearse_transport import exercise
        result.update(exercise(root,base,output,sentinel))
        result['passed']=not application_failures
        if application_failures:raise RuntimeError('Application gates failed; shutdown/transport replay retained: '+'; '.join(application_failures))
    finally:
        if created:
            try:
                m,_=inventory(base)
                for name,ident in m['services'].items():
                    with (output/(name+'-container.log')).open('wb') as log:subprocess.run(['docker','logs','--tail','2000',ident],stdout=log,stderr=subprocess.STDOUT,timeout=15)
            finally:cleanup(base)
            result['cleanup_completed']=(base/'cleanup.complete').exists()
        if sentinel:
            if call(['docker','inspect','--format','{{index .Config.Labels "atlxpres.singlehost.sentinel"}}',sentinel]).decode().strip()!=run:raise RuntimeError('sentinel ownership mismatch')
            subprocess.run(['docker','rm','-f',sentinel],check=True,timeout=30,stdout=subprocess.DEVNULL)
        (output/'result.json').write_text(json.dumps(result,indent=2));print(json.dumps(result),flush=True)

if __name__=='__main__':main()
