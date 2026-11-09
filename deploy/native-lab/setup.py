"""Guarded fresh-run setup using existing cohost tools and synthetic server caches.

No production credentials, SQL target, Docker context or arbitrary image is accepted.
The live entry point reuses this same preparation; it never downloads artifacts.
"""
import base64,datetime,hashlib,json,os,re,shutil,subprocess,sys,time
from pathlib import Path
from native import atomic,SOURCE,HERE,NativeRunner
from trial_policy import STAGES,client_hashes
from native_budget import shared

ROOT=Path('/root/atlxpres-native')
def call(args,timeout=180,**kwargs):
    if 'stdout' in kwargs:
        subprocess.run(args,check=True,timeout=timeout,**kwargs);return b''
    return subprocess.check_output(args,timeout=timeout,**kwargs)
def catalog():
    path=ROOT/'catalog.json'
    for p in (ROOT,path):
        if p.is_symlink() or p.stat().st_uid!=0 or p.stat().st_mode&0o022:raise ValueError('protected native catalog required')
    c=json.loads(path.read_text())
    if c['role'] not in ('main','database'):raise ValueError('fixed host role required')
    return c
def base_for(run):
    if not re.fullmatch('cohost-[a-f0-9]{16}',run):raise ValueError('invalid owned nonce')
    return Path('/root/atlxpres-cohost')/run
def manager(config,op,timeout=30):
    message='native-setup '+base64.b64encode(json.dumps(op).encode()).decode()
    return call(['ssh','-F',config['manager_config'],'-o','BatchMode=yes','cohost-database-manager',message],timeout=timeout)
def guard(base):
    f=base/'local.lease'
    if not f.exists() or not 0<=time.time()-f.stat().st_mtime<=5:raise RuntimeError('host-local preparation watchdog stopped')
def sql(base,statement,timeout=180):
    m=json.loads((base/'manifest.json').read_text());ident=m['services']['postgres']
    rows=json.loads(call(['docker','inspect',ident],timeout=10))
    from safety import validate_inventory
    validate_inventory(dict(run=m['run'],container_ids=[ident]),rows)
    args=['docker','exec','-i',ident,'psql','-U','lab','-d','capacity_lab','-XAtq','-v','ON_ERROR_STOP=1']
    target=call(args+['-c',"SELECT current_database()||':'||current_setting('cluster_name')"],timeout=15).decode().strip()
    if target!='capacity_lab:'+m['run']:raise ValueError('refuse SQL outside the owned disposable cluster')
    guard(base)
    return call(args,input=statement.encode(),timeout=timeout).decode().strip()
def initialize(c,run,keys,live,expiry):
    if type(live) is not bool or set(keys)!=set(('ci-main','ci-database','main-database')):raise ValueError('explicit mode and fixed test keys required')
    try:expiry_time=datetime.datetime.strptime(expiry,'%Y%m%d%H%M%SZ').replace(tzinfo=datetime.timezone.utc).timestamp()
    except (TypeError,ValueError):raise ValueError('fixed UTC key expiry required')
    if not 7500<=expiry_time-time.time()<=10860:raise ValueError('test key lifetime outside reviewed bounds')
    for name,public in keys.items():
        if not isinstance(public,str) or len(public)>1024 or not re.fullmatch(r'ssh-ed25519 [A-Za-z0-9+/=]+(?: [A-Za-z0-9_-]+)?',public):raise ValueError('fixed public test keys required')
    base=base_for(run)
    if base.exists():raise ValueError('new nonce required; existing run not overwritten')
    # One owned test at a time. Production has no cohost run label.
    existing=call(['docker','ps','-aq','--filter','label=atlxpres.cohost.run']).decode().strip()
    if existing:raise ValueError('another test inventory exists; inspect/clean that run first')
    base.mkdir(mode=0o700,parents=True)
    shutil.copytree(ROOT/'source',base/'source')
    shutil.copytree(base/'source/deploy/cohost-lab',base/'tools')
    for directory in ('exchange','tunnel'):(base/directory).mkdir(mode=0o700)
    shutil.copyfile(base/'source/deploy/capacity-lab/haproxy.cfg',base/'haproxy.cfg');(base/'haproxy.cfg').chmod(0o644)
    doc=json.loads(json.dumps(c['compose_template']).replace('cohost-0000000000000000',run).replace('cohost0000000000000000',run.replace('-','')))
    atomic(base/'compose.json',doc)
    request=dict(run=run,role=c['role'],execution_mode='vps-native',images=c['images'],image_source=c['application']['source'],image_archive_sha256=c['archive_sha256'],compose_sha256=hashlib.sha256((base/'compose.json').read_bytes()).hexdigest(),key_expiry_utc=expiry,preinstalled_release_images=([c['images']['api'],c['images']['worker']] if c['role']=='main' else []))
    atomic(base/'request.json',request)
    atomic(base/'native-origin.json',dict(created_by='native-manager',run=run,role=c['role'],source=c['source_commit']))
    atomic(base/'approval.json',dict(run=run,authorized_setup=True,authorized_image_import=True,load_authorized=False,approved_unix=time.time()))
    trial=dict(timing_policy='http-2s-observer-2.5s-v1',execution_mode='vps-native',run=run,load_authorized=live,fixture_preparation_authorized=True,spending_cap_usd=0,main_cpu=1,main_memory=2684354560,database_cpu=2,database_memory=3221225472,generator_cpu=2,generator_memory=4294967296,shared_main_memory=4294967296,lifetime_seconds=7200,approved_unix=time.time(),source_commit=c['source_commit'],image_source=c['application']['source'],stages=[list(s) for s in STAGES],client_hashes=client_hashes(base/'source'))
    atomic(base/'trial-approval.json',trial)
    os.link(c['archive'],base/'images.tar.gz')
    if c['role']=='main':
        os.link(ROOT/'verifier.bin',base/'verifier.bin')
    for name,public in keys.items():
        if name not in ('ci-main','ci-database','main-database') or not re.fullmatch(r'ssh-ed25519 [A-Za-z0-9+/=]+(?: [A-Za-z0-9_-]+)?',public):raise ValueError('fixed public test keys required')
        (base/(name+'.pub')).write_text(public+'\n');(base/(name+'.pub')).chmod(0o600)
    return base
def phase(base,name,**fields):atomic(base/'setup-progress.json',dict(run=base.name,phase=name,time=time.time(),load_started=False,**fields))
def stage(base):
    role=json.loads((base/'request.json').read_text())['role']
    for action in ('baseline','stage'):
        phase(base,role+' '+action)
        with (base/(action+'.log')).open('wb') as log:call([sys.executable,str(base/'tools/operator_setup.py'),str(base),action],timeout=900,stdout=log)
def restore(base,c):
    phase(base,'snapshot-restore')
    meta=json.loads(Path(c['snapshot_metadata']).read_text());proof=json.loads(Path(c['snapshot_proof']).read_text());dump=Path(c['snapshot'])
    if meta['sha256']!=c['application']['snapshot_sha256'] or proof.get('snapshot_sha256')!=meta['sha256'] or proof.get('restore_and_original_smoke_passed') is not True:raise ValueError('reviewed synthetic snapshot proof mismatch')
    h=hashlib.sha256()
    with dump.open('rb') as stream:
        if stream.read(5)!=b'PGDMP':raise ValueError('synthetic custom archive required')
        stream.seek(0)
        for block in iter(lambda:stream.read(1024*1024),b''):h.update(block)
    if h.hexdigest()!=meta['sha256'] or dump.stat().st_size!=meta['bytes']:raise ValueError('cached snapshot checksum/size mismatch')
    if sql(base,"SELECT count(*) FROM pg_tables WHERE schemaname='public'")!='0':raise ValueError('restore target must be empty')
    if sql(base,"SELECT current_setting('server_version_num')::int/10000")!='16':raise ValueError('qualification requires isolated PostgreSQL16')
    m=json.loads((base/'manifest.json').read_text());ident=m['services']['postgres']
    with dump.open('rb') as src,(base/'restore.log').open('wb') as log:
        process=subprocess.Popen(['docker','exec','-i',ident,'pg_restore','-U','lab','-d','capacity_lab','--single-transaction','--exit-on-error','--no-owner','--no-acl'],stdin=src,stdout=log,stderr=log)
        deadline=time.monotonic()+720
        try:
            while process.poll() is None:
                guard(base)
                if time.monotonic()>deadline:raise RuntimeError('bounded snapshot restore deadline')
                time.sleep(.25)
            if process.returncode:raise RuntimeError('snapshot restore failed; retained restore.log')
        finally:
            if process.poll() is None:process.terminate();process.wait(timeout=10)
    sys.path.insert(0,str(base/'source/deploy/singlehost-lab'))
    from snapshot import database_state
    observed=database_state(base,query=sql)
    if observed!=meta['database']:raise ValueError('full restored table counts/fingerprints/triggers differ')
    if sql(base,"SELECT marker||':'||scenario FROM capacity_lab_identity")!='disposable-compose-only:large-busy':raise ValueError('synthetic marker missing')
    # The archive is reused honestly, including its recorded age. Only renewable
    # synthetic Town Hall lifetimes are refreshed; code/traffic/gates stay fixed.
    age=time.time()-meta['created_unix']
    if age<0:raise ValueError('snapshot creation time is in the future')
    if sql(base,"SELECT count(*) FROM provider_subscriptions s JOIN capacity_lab_actors a ON a.provider=s.provider_id WHERE s.status='active' AND s.current_period_end>clock_timestamp()+interval '3 hours'")!='500':raise ValueError('cached synthetic subscriptions cannot cover the complete run')
    if sql(base,"SELECT count(*) FROM users WHERE email NOT LIKE '%@example.invalid' OR password_hash<>'unusable'")!='0':raise ValueError('snapshot must contain synthetic pre-session accounts only')
    sql(base,"UPDATE town_hall_requests SET engagement_deadline=clock_timestamp()+interval '24 hours' WHERE room_id IN(SELECT id FROM chat_rooms WHERE state='open') AND expired_at IS NULL;")
    phase(base,'isolated-migrations')
    applied=[]
    for migration in sorted((base/'source/deploy/native-lab/release-migrations').glob('*.sql')):
        name=migration.name
        if sql(base,"SELECT count(*) FROM schema_migrations WHERE name='"+name+"'")=='1':continue
        raw=migration.read_bytes()
        if hashlib.sha256(raw).hexdigest()!=c['migration_hashes'][name]:raise ValueError('pinned migration changed')
        sql(base,"BEGIN;\n"+raw.decode()+"\nINSERT INTO schema_migrations(name) VALUES('"+name+"');COMMIT;",timeout=180);applied.append(name)
    if sql(base,"SELECT count(*) FROM schema_migrations WHERE name ~ '^(108|109|110|111|112|113|114)_'")!='7':raise ValueError('isolated migrations incomplete')
    sql(base,'CREATE EXTENSION IF NOT EXISTS pg_stat_statements; ANALYZE;',timeout=180)
    atomic(base/'snapshot-restored.json',dict(restore_passed=True,snapshot_sha256=meta['sha256'],all_table_counts_and_row_fingerprints_match_before_clock_refresh=True,live_triggers_match=True,cache_age_seconds=age,synthetic_deadline_refresh='24hours on open synthetic Town Hall requests only',applied_migrations=applied,production_data_used=False))
def prepare_database(base,c):
    stage(base);restore(base,c)
    call([sys.executable,str(base/'tools/verify_cgroup.py'),str(base/'manifest.json')],timeout=30)
    phase(base,'database ready',ready=True,limits_verified=True)
def prepare_main(config):
    base=Path(config['main_base']);c=catalog();payload=json.loads((base/'database-payload.json').read_text())
    manager(config,payload,timeout=30)
    stage(base)
    # The k6 image is fetched once during authorized no-load installation only.
    image=c.get('generator_image')
    if not image:
        if config.get('live_authorized'):raise ValueError('generator not cached; refuse downloads during live execution')
        phase(base,'cache k6 image')
        with (base/'k6-cache.log').open('wb') as log:call(['docker','pull','grafana/k6:2.3.0'],timeout=180,stdout=log)
        image=call(['docker','image','inspect','grafana/k6:2.3.0','--format','{{.Id}}']).decode().strip()
        c['generator_image']=image;atomic(ROOT/'catalog.json',c)
    if call(['docker','image','inspect',image,'--format','{{.Id}}']).decode().strip()!=image:raise ValueError('cached immutable generator image unavailable')
    config['generator_image']=image;atomic(base/'native.json',config)
    phase(base,'waiting for database restore')
    deadline=time.monotonic()+900
    while True:
        guard(base)
        status=json.loads(manager(config,dict(action='status',run=base.name),timeout=15))
        atomic(base/'database-setup-progress.json',status)
        if status.get('failed'):raise RuntimeError('database preparation failed: '+status.get('error','inspect retained journal'))
        if status.get('ready'):break
        if time.monotonic()>deadline:raise RuntimeError('database preparation exceeded bounded deadline')
        time.sleep(2)
    phase(base,'verify private database connection')
    call([sys.executable,str(base/'tools/operator_setup.py'),str(base),'verify'],timeout=180)
    runner=NativeRunner(config)
    for role in ('main','database'):
        observed=json.loads(runner.ssh(role,'status',timeout=15))
        if observed.get('role')!=role or observed.get('run')!=base.name or observed.get('age_seconds',999)>3:raise ValueError('independent host watchdog not fresh')
    m=json.loads((base/'manifest.json').read_text());m['runtime_verification_authorized']=True;atomic(base/'manifest.json',m)
    for name in ('api1','api2','gateway','message-fanout'):runner.service(name)
    deadline=time.monotonic()+60
    while True:
        guard(base)
        try:
            body=runner.control('main','exec','api1',['curl','-fsS','--max-time','3','http://gateway:8080/api/v1/ready'])
            if json.loads(body).get('ok') is True:break
        except (subprocess.CalledProcessError,ValueError):pass
        if time.monotonic()>deadline:raise ValueError('isolated API readiness failed')
        time.sleep(1)
    for name,key in (('api1','api_sha256'),('api2','api_sha256'),('message-fanout','worker_sha256')):
        if runner.control('main','binary-identity',name).decode().split()[0]!=config['application'][key]:raise ValueError('running release binary mismatch: '+name)
    deadline=time.monotonic()+60
    while True:
        guard(base)
        try:
            ready=runner.control('main','exec','message-fanout',['curl','-fsS','--max-time','3','http://127.0.0.1:9090/api/v1/ready'])
            if json.loads(ready).get('ok') is True:break
        except (subprocess.CalledProcessError,ValueError):pass
        if time.monotonic()>deadline:raise ValueError('isolated fanout worker readiness failed')
        time.sleep(1)
    call([sys.executable,str(base/'tools/verify_cgroup.py'),str(base/'manifest.json')],timeout=30)
    if status.get('limits_verified') is not True:raise ValueError('database effective limits not verified')
    atomic(base/'native-readiness.json',dict(run=base.name,code_hashes=config['code_hashes'],application=config['application'],isolated_snapshot_restored=True,schema_114_verified=True,two_api_checksums_verified=True,worker_checksum_verified=True,both_host_watchdogs_verified=True,resource_caps_verified=True,peer_connection_verified=True))
    phase(base,'ready',ready=True)
    (base/'setup.complete').touch()
