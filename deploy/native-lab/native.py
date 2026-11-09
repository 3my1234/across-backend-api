"""Host-native adapter for the unchanged cohost workload. No live install/load by default.

Main-host operations are local. The database uses the existing restricted,
host-key-pinned, noninteractive test connection. No Windows/CI heartbeat exists.
"""
import argparse,concurrent.futures,hashlib,json,os,re,signal,subprocess,sys,time
from pathlib import Path

HERE=Path(__file__).resolve().parent
SOURCE=HERE.parents[1]
sys.path.insert(0,str(SOURCE/'deploy/cohost-lab'))
from trial_runner import Runner,execute_sequence
from trial_policy import STAGES,validate,client_hashes,peer_progress

def atomic(path,value):
    pending=path.with_suffix('.next')
    pending.write_text(json.dumps(value));pending.chmod(0o600);pending.replace(path)

def load(path,live=True):
    if path.is_symlink() or not path.is_file():raise ValueError('regular native configuration required')
    path=path.absolute()
    if path.resolve()!=path:raise ValueError('symlinked native configuration parent')
    config=json.loads(path.read_text());run=config.get('run','')
    if not re.fullmatch('cohost-[a-f0-9]{16}',run):raise ValueError('invalid run nonce')
    base=Path('/root/atlxpres-cohost')/run
    if path!=base/'native.json' or config.get('main_base')!=str(base):raise ValueError('fixed owned main run directory required')
    if not re.fullmatch(r'(?:[\w./:-]+@)?sha256:[0-9a-f]{64}',config.get('generator_image','')):raise ValueError('immutable generator image required')
    if config.get('generator_ip')!='172.29.248.21':raise ValueError('original trusted synthetic generator address required')
    if config.get('network')!=run+'-main_lab':raise ValueError('owned private generator network required')
    if config.get('stages')!=[list(s) for s in STAGES]:raise ValueError('original stages must remain unchanged')
    app=config.get('application',{})
    if not re.fullmatch('[a-f0-9]{40}',app.get('source','')) or any(not re.fullmatch('[a-f0-9]{64}',app.get(k,'')) for k in ('api_sha256','worker_sha256','snapshot_sha256')):raise ValueError('pinned API, worker and snapshot identities required')
    for p in (path,base,Path(config['peer_config'])):
        if p.is_symlink() or not p.exists() or (os.name=='posix' and (p.stat().st_uid!=0 or p.stat().st_mode&0o022)):raise ValueError('unsafe native package path/owner/mode')
    if config.get('client_hashes')!=client_hashes(SOURCE):raise ValueError('qualified client/policy source changed')
    for name,digest in config.get('code_hashes',{}).items():
        target=(SOURCE/name).resolve()
        if not target.is_relative_to(SOURCE) or hashlib.sha256(target.read_bytes()).hexdigest()!=digest:raise ValueError('native package source checksum mismatch')
        if name.startswith('deploy/cohost-lab/') and hashlib.sha256((base/'tools'/Path(name).name).read_bytes()).hexdigest()!=digest:raise ValueError('installed host-control helper differs from proved source')
    required=('deploy/native-lab/native.py','deploy/native-lab/launch.py','deploy/native-lab/local.py','deploy/cohost-lab/trial_runner.py')
    if not all(n in config.get('code_hashes',{}) for n in required):raise ValueError('native source inventory missing')
    if live:
        if os.name!='posix' or os.geteuid()!=0:raise ValueError('Linux root required')
        proof=base/'native-lifecycle.json'
        if proof.is_symlink() or hashlib.sha256(proof.read_bytes()).hexdigest()!=config.get('lifecycle_sha256'):raise ValueError('pinned Linux lifecycle proof required')
        observed=json.loads(proof.read_text())
        if observed.get('passed') is not True or observed.get('code_hashes')!=config['code_hashes']:raise ValueError('lifecycle proof does not match installed native sources')
        for name in ('detached','term_cleanup','kill_cleanup','deadline_cleanup','peer_loss_stops_job','unrelated_container_preserved','shared_budget_verified'):
            if observed.get(name) is not True:raise ValueError('missing lifecycle assertion: '+name)
        # A lifecycle proof alone does not certify the current API, restored DB or peer.
        readiness=json.loads((base/'native-readiness.json').read_text())
        if readiness.get('run')!=run or readiness.get('code_hashes')!=config['code_hashes'] or readiness.get('application')!=config.get('application'):raise ValueError('prepared runtime identity mismatch')
        for name in ('isolated_snapshot_restored','schema_114_verified','two_api_checksums_verified','worker_checksum_verified','both_host_watchdogs_verified','resource_caps_verified','peer_connection_verified'):
            if readiness.get(name) is not True:raise ValueError('unverified prepared runtime: '+name)
    return config

class NativeRunner(Runner):
    def __init__(self,config):
        self.native=config;self.base=Path(config['main_base'])
        approval=json.loads((self.base/'trial-approval.json').read_text())
        if approval.get('execution_mode')!='vps-native':raise ValueError('native1CPU API/2CPU database approval required')
        if approval.get('image_source')!=config.get('application',{}).get('source'):raise ValueError('approved application source mismatch')
        super().__init__(SOURCE,Path(config['peer_config']),approval,self.base/'results')
    def control_process(self,role,command):
        if role=='main':return [sys.executable,str(HERE/'local.py'),str(self.base),command]
        if role!='database':raise ValueError('unknown host role')
        return ['ssh','-F',str(self.config),'-o','BatchMode=yes','-o','NumberOfPasswordPrompts=0','-o','StrictHostKeyChecking=yes','cohost-database',command]
    def ssh(self,role,cmd,timeout=30,input=None,destination=None):
        args=self.control_process(role,cmd)
        if destination is None:return subprocess.check_output(args,input=input,timeout=timeout)
        with destination.open('wb') as out:subprocess.run(args,input=input,stdout=out,check=True,timeout=timeout)
    def generator_network(self):
        return ['--network',self.native['network'],'--ip',self.native['generator_ip']]
    def generator_image(self):return self.native['generator_image']
    def generator_parent(self):
        from native_budget import shared
        return ['--cgroup-parent',shared(self.approval['run'])]
    def connect_gateway(self):
        # Never bind or target main-host8080: production already owns that port.
        deadline=time.monotonic()+60
        while True:
            self.check()
            try:
                body=self.control('main','exec','api1',['curl','-fsS','--max-time','3','http://gateway:8080/api/v1/ready'])
                if json.loads(body).get('ok') is True:break
            except (subprocess.CalledProcessError,ValueError):pass
            if time.monotonic()>=deadline:raise ValueError('owned gateway not ready within60seconds')
            time.sleep(1)
    def check(self):
        super().check()
        state=json.loads((self.base/'native-peer.json').read_text())
        if state.get('run')!=self.approval['run'] or state.get('failed') or not 0<=time.time()-state['observed_unix']<=5:raise RuntimeError('independent native peer monitor stale/failed')
    def prepare(self):
        # Restoration and migrations belong to bounded host setup, not every load stage.
        # Never seed again or accept a production DB as a substitute for the fixture.
        if self.sql("SELECT current_database()||':'||current_setting('cluster_name')")!='capacity_lab:'+self.approval['run']:raise ValueError('wrong disposable database')
        if self.sql("SELECT marker||':'||scenario FROM capacity_lab_identity")!='disposable-compose-only:large-busy':raise ValueError('synthetic fixture identity missing')
        if self.sql("SELECT count(*) FROM schema_migrations WHERE name ~ '^(108|109|110|111|112|113|114)_'")!='7':raise ValueError('isolated schema108-114 not verified')
        if self.sql("SELECT (SELECT count(*) FROM capacity_lab_actors)||':'||(SELECT count(*) FROM products)||':'||(SELECT count(*) FROM chat_rooms)||':'||(SELECT count(*) FROM chat_messages)")!='500:5000:100050:1025000':raise ValueError('original full synthetic fixture missing')
        for name in ('exporter','verifier','lifecycle','queue-check'):self.service(name)
        self.lab('prepare-sessions')
        self.lab('notification-prepare',destination=self.output/'notification-preparation.log')
        self.sql('CREATE EXTENSION IF NOT EXISTS pg_stat_statements; ANALYZE;')
        for name in ('api1','api2','gateway','notification-provider','message-fanout'):self.service(name)
        self.connect_gateway()
        for name,key in (('api1','api_sha256'),('api2','api_sha256'),('message-fanout','worker_sha256')):
            actual=self.control('main','binary-identity',name).decode().split()[0]
            if actual!=self.native['application'][key]:raise ValueError('running executable mismatch: '+name)
        self.lab('lifecycle','lifecycle',self.output/'lifecycle.jsonl')
        self.lab('queue-check','queue-check',self.output/'queue-check.jsonl')
    def measure(self,stage):
        atomic(self.base/'progress.json',dict(run=self.approval['run'],stage=stage[0],sessions=stage[2],state='running',time=time.time()))
        super().measure(stage)
        self.fanout(self.output/stage[0])
        self.completed.append(stage)
        atomic(self.base/'progress.json',dict(run=self.approval['run'],stage=stage[0],sessions=stage[2],state='all original gates passed',time=time.time()))
    def fanout(self,output):
        # Reuse the independent durable-fanout recipient ledger. Never mistake
        # HTTP acknowledgement for completed, exactly-once notification fan-out.
        import uuid
        sys.path.insert(0,str(SOURCE/'deploy/singlehost-lab'))
        from message_fanout_replay import LEDGER_SQL,require_ledger
        deadline=time.monotonic()+15
        while True:
            self.check()
            observed=json.loads(self.sql("SELECT json_build_object('jobs',count(*),'pending',count(*) FILTER(WHERE completed_at IS NULL),'failed',count(*) FILTER(WHERE failed_at IS NOT NULL),'oldest_pending_seconds',coalesce(max(extract(epoch FROM clock_timestamp()-created_at)) FILTER(WHERE completed_at IS NULL),0),'max_completed_lag_seconds',coalesce(max(extract(epoch FROM completed_at-created_at)),0)) FROM message_notification_jobs"))
            if observed['failed'] or observed['pending']==0 or time.monotonic()>=deadline:break
            time.sleep(.5)
        stage_run=str(uuid.UUID(json.loads((output/'summary.json').read_text())['run_id']))
        ledger=json.loads(self.sql(LEDGER_SQL.replace('run_id::text',"'"+stage_run+"'::text")))
        atomic(output/'message-fanout-ledger.json',dict(**observed,**ledger))
        require_ledger('async',observed,ledger)
    def start(self):
        # Independent systemd peer service refreshes leases. The coordinator never does.
        deadline=time.monotonic()+15
        while True:
            try:self.check();break
            except (OSError,ValueError,RuntimeError):
                if time.monotonic()>deadline:raise
                time.sleep(.25)
        with concurrent.futures.ThreadPoolExecutor(2) as pool:list(pool.map(lambda role:self.ssh(role,'activate',timeout=60),('main','database')))
        self.activation_done=True
    def cleanup(self):
        self.stop.set()
        for child in self.children:
            if child.poll() is None:
                try:os.killpg(child.pid,signal.SIGTERM)
                except ProcessLookupError:pass
        cleanup(self.native)

def peer(config):
    runner=NativeRunner(config);previous={};base=Path(config['main_base'])
    try:
        # Cold connection setup is not the recurring heartbeat measurement.
        with concurrent.futures.ThreadPoolExecutor(2) as pool:
            statuses=list(pool.map(lambda role:json.loads(runner.ssh(role,'status',timeout=15)),('main','database')))
        peer_progress(statuses,{},config['run'])
        while True:
            # Cleanup stops both host guards. Stay alive until ExecStopPost ends
            # without refreshing leases, so BindsTo cannot interrupt that cleanup.
            if (base/'native-draining').exists():time.sleep(.5);continue
            with concurrent.futures.ThreadPoolExecutor(2) as pool:
                statuses=list(pool.map(lambda role:json.loads(runner.ssh(role,'status',timeout=2)),('main','database')))
            progress=peer_progress(statuses,previous,config['run'])
            if progress is not None:
                previous=progress
                with concurrent.futures.ThreadPoolExecutor(2) as pool:list(pool.map(lambda role:runner.ssh(role,'lease',timeout=2),('main','database')))
                atomic(base/'native-peer.json',dict(run=config['run'],observed_unix=time.time(),sequences=previous,failed=False))
            time.sleep(.5)
    except BaseException:
        if (base/'native-draining').exists():
            while True:time.sleep(.5)  # Never refresh a lease during teardown.
        atomic(base/'native-peer.json',dict(run=config['run'],observed_unix=time.time(),failed=True))
        # Both pre-existing local watchdogs/reapers retain independent stop deadlines.
        raise

def stop_generator(config):
    found=subprocess.run(['docker','inspect','cohost-generator'],capture_output=True,text=True,timeout=10)
    if found.returncode==0:
        item=json.loads(found.stdout)[0]
        if item['Config'].get('Labels',{}).get('atlxpres.cohost.run')!=config['run']:return 'foreign generator; preserved'
        subprocess.run(['docker','stop','--timeout','5',item['Id']],check=True,capture_output=True,timeout=20)
        subprocess.run(['docker','rm',item['Id']],check=True,capture_output=True,timeout=15)
    return None

def cleanup(config,database_confirmed=False):
    runner=NativeRunner(config);result={}
    (runner.base/'native-draining').touch()
    from native_budget import shared
    parent=Path('/sys/fs/cgroup')/shared(config['run'])
    try:
        atomic(runner.base/'shared-budget-final.json',{n:(parent/n).read_text() for n in ('memory.max','memory.current','memory.events','cpu.max','cpu.stat')})
    except OSError:pass  # A removed cgroup is not permission to skip owned cleanup.
    saved=Path(config['main_base'])/'native-cleanup.json'
    if saved.exists() and (runner.base/'cleanup.complete').exists():
        previous=json.loads(saved.read_text())
        if previous.get('run')==config['run'] and previous.get('hosts')==dict(database='confirmed',main='confirmed'):return
    # Never stop a generator owned by a different run, even on coordinator SIGKILL.
    warning=stop_generator(config)
    if warning:result['generator']=warning
    # Remote cleanup first: main cleanup removes the test-only database identity key.
    if database_confirmed:result['database']='confirmed'
    for role in (('main',) if database_confirmed else ('database','main')):
        try:runner.ssh(role,'cleanup',timeout=180);result[role]='confirmed'
        except Exception as e:result[role]='unconfirmed: '+type(e).__name__
    atomic(Path(config['main_base'])/'native-cleanup.json',dict(run=config['run'],hosts=result))
    if any(v!='confirmed' for v in result.values()):raise RuntimeError('cleanup incomplete; preserve evidence; host-local reapers remain independent')
    from install_ci_bundle import cleanup as socket_cleanup
    socket_cleanup(runner.base)
    for name in ('ci-main.key','ci-database.key'):(runner.base/name).unlink(missing_ok=True)

def execute(config):
    runner=NativeRunner(config);validate(runner.approval,config['run'])
    runner.completed=[]
    runner.output.mkdir(mode=0o700,exist_ok=False)
    atomic(runner.output/'native-provenance.json',dict(config=config,client_hashes=client_hashes(SOURCE),stages=STAGES))
    def interrupted(*_):raise KeyboardInterrupt('native coordinator interrupted')
    signal.signal(signal.SIGTERM,interrupted);signal.signal(signal.SIGINT,interrupted)
    try:execute_sequence(runner)
    except BaseException as error:
        atomic(runner.base/'result.json',dict(passed=False,completed_stages=runner.completed,reason=type(error).__name__+': '+str(error),capacity_500_qualified=False))
        raise
    atomic(runner.output/'trial-passed.json',dict(passed=True,stages=STAGES,production_capacity_claim=False))
    atomic(runner.base/'result.json',dict(passed=True,completed_stages=runner.completed,capacity_500_qualified=True,scope='this exact synthetic workload and resource allocation; no1000+ claim'))

def main():
    p=argparse.ArgumentParser();p.add_argument('action',choices=('plan','peer','execute','cleanup'));p.add_argument('config',type=Path,nargs='?');a=p.parse_args()
    if a.action=='plan':print(json.dumps(dict(stages=STAGES,load_started=False,live_transport='main local; existing restricted noninteractive database peer',generator_network='owned private Docker network; never host8080'),indent=2));return
    config=load(a.config)
    {'peer':peer,'execute':execute,'cleanup':cleanup}[a.action](config)
if __name__=='__main__':main()
