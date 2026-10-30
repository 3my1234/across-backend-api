"""Free Linux CI coordinator. --plan is offline; execution needs explicit trial approval.
No production target is accepted. Existing k6 client, policies and Go verifiers are used.
"""
import argparse,base64,concurrent.futures,hashlib,json,os,signal,subprocess,sys,threading,time
from pathlib import Path
from trial_policy import STAGES,validate,client_hashes

class Runner:
    def __init__(self,root,config,approval,output):
        self.root=root;self.config=config;self.approval=approval;self.output=output
        self.children=[];self.stop=threading.Event();self.error=None;self.previous={};self.activation_done=False
    def ssh(self,role,cmd,timeout=30,input=None,destination=None):
        args=['ssh','-F',str(self.config),'cohost-'+role,cmd]
        if destination is None:return subprocess.check_output(args,input=input,timeout=timeout)
        with destination.open('wb') as out:subprocess.run(args,input=input,stdout=out,check=True,timeout=timeout)
    def control(self,role,action,service,args=None,timeout=30,input=None,destination=None):
        op=dict(action=action,service=service)
        if args is not None:op['args']=args
        cmd='control '+base64.b64encode(json.dumps(op).encode()).decode()
        return self.ssh(role,cmd,timeout,input,destination)
    def lab(self,command,service='exporter',destination=None,timeout=750):
        try:return self.control('main','exec',service,['/app/lab',command],timeout,destination=destination)
        except Exception:
            # Report the initiating safety failure instead of only the later
            # SQL disconnect caused by watchdog cleanup during preparation.
            self.check()
            raise
    def sql(self,sql):
        return self.control('database','exec','postgres',['psql','-U','lab','-d','capacity_lab','-XAt','-v','ON_ERROR_STOP=1'],input=sql.encode(),timeout=60).decode().strip()
    def check(self):
        if self.error:raise RuntimeError('independent watchdog/relay stopped: '+str(self.error))
    def leases(self):
        from trial_policy import peer_progress
        statuses=[];phase='initial'
        try:
            while not self.stop.is_set():
                phase='status'
                with concurrent.futures.ThreadPoolExecutor(2) as pool:
                    statuses=list(pool.map(lambda r:json.loads(self.ssh(r,'status',timeout=2)),('main','database')))
                phase='validate'
                progress=peer_progress(statuses,self.previous,self.approval['run'])
                if progress is None:
                    self.stop.wait(.5);continue
                self.previous=progress
                phase='lease'
                with concurrent.futures.ThreadPoolExecutor(2) as pool:list(pool.map(lambda r:self.ssh(r,'lease',timeout=2),('main','database')))
                self.stop.wait(.5)
        except BaseException as error:
            self.error=error
            report=dict(observed_unix=time.time(),phase=phase,error_class=type(error).__name__,error=str(error),last_statuses=statuses,last_accepted_sequences=self.previous)
            print('Peer relay stopped: '+json.dumps(report),file=sys.stderr,flush=True)
            try:(self.output/'peer-relay-failure.json').write_text(json.dumps(report,indent=2))
            except OSError:pass # Keep the original safety failure if evidence disk fails.
            subprocess.run(['docker','stop','--timeout','2','cohost-generator'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
            for process in self.children:
                if process.poll() is None:
                    try:os.killpg(process.pid,signal.SIGTERM)
                    except ProcessLookupError:pass
    def telemetry(self,public,stop):
        try:
            while not stop.is_set():
                for role in ('main','database'):
                    raw=self.ssh(role,'observations',timeout=30)
                    with (public/(role+'-resources.jsonl')).open('ab') as out:out.write(raw.rstrip()+b'\n')
                with (public/'database-activity.jsonl').open('a') as out:
                    out.write(self.sql("SELECT json_build_object('time',clock_timestamp(),'clients',count(*),'active',count(*) FILTER(WHERE state='active'),'lock_waiters',count(*) FILTER(WHERE wait_event_type='Lock')) FROM pg_stat_activity WHERE backend_type='client backend';")+'\n')
                with (public/'queue-age.jsonl').open('a') as out:
                    out.write(self.sql("SELECT json_build_object('time',clock_timestamp(),'email_pending',(SELECT count(*) FROM email_outbox WHERE status IN('pending','retry','sending')),'push_pending',(SELECT count(*) FROM notification_push_deliveries WHERE status IN('pending','retry','sending')),'email_oldest_due_seconds',greatest(0,coalesce(extract(epoch FROM clock_timestamp()-(SELECT next_attempt_at FROM email_outbox WHERE status IN('pending','retry') ORDER BY next_attempt_at LIMIT 1)),0)),'push_oldest_due_seconds',greatest(0,coalesce(extract(epoch FROM clock_timestamp()-(SELECT next_attempt_at FROM notification_push_deliveries WHERE status IN('pending','retry') ORDER BY next_attempt_at LIMIT 1)),0)));")+'\n')
                stop.wait(5)
        except BaseException as error:self.error=RuntimeError('resource observer: '+str(error))
    def start(self):
        # Establish pinned SSH masters before applying the strict recurring
        # heartbeat deadline. Cold authentication is not a runtime heartbeat.
        # This read-only handshake cannot activate resources or refresh leases.
        from trial_policy import peer_progress
        with concurrent.futures.ThreadPoolExecutor(2) as pool:
            statuses=list(pool.map(lambda r:json.loads(self.ssh(r,'status',timeout=15)),('main','database')))
        peer_progress(statuses,{},self.approval['run'])
        # Do not refresh leases until fresh advancing status exists on BOTH hosts.
        thread=threading.Thread(target=self.leases,daemon=True);thread.start();self.children_thread=thread
        deadline=time.monotonic()+10
        while not self.previous:
            self.check()
            if time.monotonic()>deadline:raise RuntimeError('initial watchdog lease unavailable')
            time.sleep(.1)
        with concurrent.futures.ThreadPoolExecutor(2) as pool:list(pool.map(lambda r:self.ssh(r,'activate',timeout=60),('main','database')))
        self.check();self.activation_done=True
    def service(self,name):self.control('main','start',name)
    def prepare(self):
        for name in ('seed','exporter','verifier','lifecycle','queue-check'):self.service(name)
        self.lab('seed','seed',self.output/'seed.log')
        # Prepare emits tokens: retain privately on the host, never a public artifact.
        self.lab('prepare-sessions')
        self.lab('notification-prepare',destination=self.output/'notification-preparation.log')
        self.sql('CREATE EXTENSION IF NOT EXISTS pg_stat_statements; ANALYZE;')
        for name in ('api1','api2','gateway'):self.service(name)
        self.ssh('main','gateway-relay')
        tunnel=subprocess.Popen(['ssh','-F',str(self.config),'-N','-L','127.0.0.1:8080:127.0.0.1:58080','cohost-main'],start_new_session=True)
        self.children.append(tunnel)
        deadline=time.monotonic()+60
        while True:
            self.check()
            try:
                import urllib.request
                with urllib.request.urlopen('http://127.0.0.1:8080/api/v1/ready',timeout=3) as response:
                    if json.load(response).get('ok') is True:break
            except Exception:
                if time.monotonic()>deadline:raise
                time.sleep(1)
        self.lab('lifecycle','lifecycle',self.output/'lifecycle.jsonl')
        self.lab('queue-check','queue-check',self.output/'queue-check.jsonl')
    def fixture(self,directory):
        self.ssh('main','publish-fixture')
        for name in ('fixtures.json','run.json'):self.ssh('main','download '+name,destination=directory/name)
    def recover(self,path):
        with path.open('ab') as out:
            for tick in range(12):
                self.check()
                out.write(self.control('main','exec','api2',['curl','-fsS','--max-time','3','http://gateway:8080/api/v1/ready']))
                time.sleep(5)
    def measure(self,stage):
        name,profile,target,seconds=stage;public=self.output/name;public.mkdir()
        private=self.output.parent/('private-'+self.approval['run'])/name;private.mkdir(parents=True,mode=0o700)
        self.fixture(private)
        self.sql('SELECT pg_stat_statements_reset();')
        resource_stop=threading.Event()
        resources=threading.Thread(target=self.telemetry,args=(public,resource_stop),daemon=True);resources.start()
        permit=dict(authorized=True,environment='isolated-staging',profile=profile,max_sessions=target,max_duration_seconds=seconds,spending_cap_usd=0,approved_at=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime(self.approval['approved_unix'])))
        (private/'approval.json').write_text(json.dumps(permit))
        import shutil
        suite=private/'suite';suite.mkdir()
        for original in (self.root/'deploy/session-lab').glob('*.mjs'):shutil.copyfile(original,suite/original.name)
        sys.path.insert(0,str(self.root/'deploy/notification-lab'))
        from generator_prewarm import candidate,recover
        original=(self.root/'deploy/session-lab/sessions.js').read_text()
        generated=candidate(original)
        if recover(generated)!=original:raise RuntimeError('qualified local prewarm is not reversible')
        expected='dad72e9b2b8dd7dd9fca94bb6a0d6856d4081a49d301b53e3e133c127d01ea49'
        if hashlib.sha256(generated.encode()).hexdigest()!=expected:raise RuntimeError('executed generator differs from qualified source')
        (suite/'sessions.js').write_text(generated)
        (public/'executed-client.sha256').write_text(expected)
        args=['docker','run','--user','0:0','--label','atlxpres.cohost.run='+self.approval['run'],'--name','cohost-generator','--network','host','--add-host','gateway:127.0.0.1',
            '--cpus','2','--memory','4g','--memory-swap','4g','--pids-limit','512',
            '-v',str(suite)+':/suite:ro','-v',str(private)+':/exchange',
            '-e','K6_NO_USAGE_REPORT=true','-e','K6_NO_COLOR=true','-e','LAB_SESSION_AUTH_MODE=established',
            '-e','LAB_SESSION_PROFILE='+profile,'-e','LAB_SESSION_TARGET='+str(target),'-e','LAB_SESSION_MIX=mixed',
            '-e','LAB_SESSION_APPROVAL=/exchange/approval.json','grafana/k6:2.3.0',
            'run','--out','json=/exchange/timeseries.json','/suite/sessions.js']
        observer_command='control '+base64.b64encode(json.dumps(dict(action='exec',service='exporter',args=['/app/lab','observe-sessions'])).encode()).decode()
        observer_out=(public/'pools.jsonl').open('wb')
        observer=subprocess.Popen(['ssh','-F',str(self.config),'cohost-main',observer_command],stdout=observer_out,stderr=subprocess.DEVNULL,start_new_session=True)
        self.children.append(observer)
        log=public/'k6.log' 
        with log.open('wb') as out:
            process=subprocess.Popen(args,stdout=out,stderr=subprocess.STDOUT,start_new_session=True);self.children.append(process)
            from generator_monitor import sample as sample_generator
            genstop=threading.Event()
            genmonitor=threading.Thread(target=sample_generator,args=(process,genstop,public/'generator-cgroups.jsonl'),daemon=True);genmonitor.start()
            deadline=time.monotonic()+seconds+(180 if target<=100 else 600)+90
            aborted=None
            try:
                while process.poll() is None:
                    self.check()
                    if time.monotonic()>deadline:
                        os.killpg(process.pid,signal.SIGTERM);raise RuntimeError('generator exceeded original setup/workload/recovery bound')
                    # Generator and host metrics are outside the k6 process and include its actual cgroup allocation.
                    with (public/'generator-stats.jsonl').open('ab') as stats:
                        subprocess.run(['docker','stats','--no-stream','--format','{{json .}}','cohost-generator'],stdout=stats,stderr=subprocess.DEVNULL,timeout=10)
                    time.sleep(3)
            except BaseException as error:
                aborted=repr(error)
                subprocess.run(['docker','stop','--timeout','2','cohost-generator'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
                if process.poll() is None:
                    os.killpg(process.pid,signal.SIGTERM)
                    try:process.wait(timeout=15)
                    except subprocess.TimeoutExpired:os.killpg(process.pid,signal.SIGKILL);process.wait(timeout=5)
            code=process.returncode
            genstop.set();genmonitor.join(timeout=10)
            if genmonitor.is_alive():aborted='generator sampler failed to stop'
        (public/'k6-exit-code.txt').write_text(str(code if code>=0 else 128-code))
        if aborted:(public/'aborted.json').write_text(json.dumps(dict(error=aborted)))
        import shutil
        for filename in ('summary.json','timeseries.json'):
            if (private/filename).exists():shutil.copyfile(private/filename,public/filename)
        resource_stop.set();resources.join(timeout=65)
        if resources.is_alive():raise RuntimeError('resource observer did not finish')
        try:
            (public/'sql-statements.json').write_text(self.sql("SELECT coalesce(json_agg(s),'[]') FROM (SELECT queryid::text,calls,total_exec_time,mean_exec_time,max_exec_time,rows,query FROM pg_stat_statements WHERE dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY total_exec_time DESC LIMIT 100)s;"))
        except Exception as error:(public/'sql-collection-error.txt').write_text(str(error))
        failures=([aborted] if aborted else [])
        try:
            inspect=json.loads(subprocess.check_output(['docker','inspect','cohost-generator'],text=True,timeout=10))[0]
            if inspect['Config']['Labels'].get('atlxpres.cohost.run')!=self.approval['run']:raise RuntimeError('wrong generator owner')
            evidence=dict(state=inspect['State'],cpu_nanos=inspect['HostConfig']['NanoCpus'],memory=inspect['HostConfig']['Memory'],swap=inspect['HostConfig']['MemorySwap'])
            (public/'generator-inspection.json').write_text(json.dumps(evidence))
            if evidence['state'].get('OOMKilled') or evidence['memory']!=4*2**30 or evidence['cpu_nanos']!=2000000000:failures.append('generator resource/OOM gate')
            subprocess.run(['docker','rm',inspect['Id']],check=True,stdout=subprocess.DEVNULL,timeout=15)
        except Exception as error:failures.append('generator inspection: '+str(error))
        if self.error:failures.append('observer: '+str(self.error))
        # Reconciliation is attempted even when an original performance gate aborts.
        for filename in ('summary.json','k6.log','k6-exit-code.txt'):
            if (public/filename).exists():
                try:self.ssh('main','upload '+filename,input=(public/filename).read_bytes(),timeout=180)
                except Exception as error:failures.append('evidence upload '+filename+': '+str(error))
        try:self.lab('verify-sessions','verifier',public/'reconciliation.jsonl')
        except BaseException as error:failures.append('reconciliation: '+str(error))
        try:self.recover(public/'recovery.log')
        except BaseException as error:failures.append('recovery: '+str(error))
        if code!=0:failures.append('original k6 gate exit '+str(code))
        try:
            from generator_monitor import gate as generator_gate
            generator_gate(public,seconds,target,profile)
        except Exception as error:failures.append('actual generator: '+str(error))
        if not failures:
            with (public/'gate.json').open('wb') as gate:
                subprocess.run(['node',str(self.root/'deploy/cohost-lab/stage_gate.mjs'),str(public/'summary.json'),str(public/'reconciliation.jsonl'),profile,str(target)],check=True,stdout=gate)
        if observer.poll() is not None:failures.append('pool observer ended before the full stage/recovery window')
        self.control('main','stop','exporter');self.service('exporter')
        try:observer.wait(timeout=10)
        except subprocess.TimeoutExpired:
            os.killpg(observer.pid,signal.SIGTERM);failures.append('pool observer failed to stop')
        observer_out.close()
        if failures:raise RuntimeError('; '.join(failures))
        self.sql("UPDATE capacity_lab_session_run SET run_id=gen_random_uuid();")
    def notifications(self):
        # Same five-minute send-drain and real 15-minute receipt eligibility as original harness.
        deadline=time.monotonic()+305
        while True:
            self.check()
            remaining=int(self.sql("SELECT (SELECT count(*) FROM email_outbox WHERE status IN('pending','retry','sending'))+(SELECT count(*) FROM notification_push_deliveries WHERE status IN('pending','retry','sending'));"))
            with (self.output/'queue-drain.jsonl').open('a') as out:out.write(json.dumps(dict(time=time.time(),remaining=remaining))+'\n')
            if remaining==0:break
            if time.monotonic()>deadline:raise RuntimeError('original send-drain limit exceeded')
            time.sleep(5)
        self.lab('notification-verify',destination=self.output/'notification-before-receipts.json')
        before=json.loads((self.output/'notification-before-receipts.json').read_text())
        if any(before['sent_without_mock_ack'].values()) or not set(before['email_status'])<={'sent'} or not set(before['push_status'])<={'sent','delivered'}:raise RuntimeError('provider acknowledgement/status gate')
        deadline=time.monotonic()+1205
        while True:
            self.check()
            remaining=int(self.sql("SELECT count(*) FROM notification_push_deliveries WHERE status='sent' AND receipt_checked_at IS NULL;"))
            with (self.output/'receipt-tail.jsonl').open('a') as out:out.write(json.dumps(dict(time=time.time(),remaining=remaining))+'\n')
            if remaining==0:break
            if time.monotonic()>deadline:raise RuntimeError('original receipt tail limit exceeded')
            time.sleep(5)
        self.lab('notification-verify',destination=self.output/'notification-after-receipts.json')
        final=json.loads((self.output/'notification-after-receipts.json').read_text())
        if final['due_receipt_backlog']!=0 or not set(final['push_status'])<={'delivered'} or any(final['sent_without_mock_ack'].values()):raise RuntimeError('original recipient receipt gate')
        self.ssh('main','download provider-journal.jsonl',destination=self.output/'provider-journal.jsonl',timeout=180)
    def cleanup(self):
        for role in ('main','database'):
            try:self.ssh(role,'observations-full',destination=self.output/(role+'-observations.json'))
            except Exception as error:(self.output/(role+'-observation-error.txt')).write_text(str(error))
        self.stop.set()
        for process in self.children:
            if process.poll() is None:
                try:os.killpg(process.pid,signal.SIGTERM)
                except ProcessLookupError:pass
        subprocess.run(['docker','stop','--timeout','2','cohost-generator'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
        cleanup_errors=[]
        for role in ('main','database'):
            try:self.ssh(role,'cleanup',timeout=180,destination=self.output/(role+'-cleanup.log'))
            except Exception as error:
                (self.output/(role+'-cleanup-error.txt')).write_text(str(error));cleanup_errors.append(role)
        if cleanup_errors:raise RuntimeError('cleanup not confirmed: '+','.join(cleanup_errors))

def execute_sequence(runner):
    try:
        runner.start();runner.prepare()
        for stage in STAGES:
            runner.check()
            if stage[0]=='sustained-500':
                runner.service('notification-provider');time.sleep(3)
                runner.control('main','exec','notification-provider',['curl','-fsS','http://localhost:8081/stats'])
                runner.service('notification-worker')
            runner.measure(stage)
        runner.notifications()
    finally:runner.cleanup()

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--plan',action='store_true');ap.add_argument('--execute',action='store_true')
    ap.add_argument('--approval',type=Path);ap.add_argument('--ssh-config',type=Path);ap.add_argument('--output',type=Path,default=Path('cohost-evidence'));a=ap.parse_args()
    if not a.execute:
        print(json.dumps(dict(stages=STAGES,application_seconds=sum(s[3] for s in STAGES),load_started=False),indent=2));return
    if os.name!='posix' or os.environ.get('GITHUB_ACTIONS')!='true':raise ValueError('qualified free Linux CI only')
    approval=json.loads(a.approval.read_text());validate(approval,approval.get('run',''))
    root=Path(__file__).resolve().parents[2]
    if not a.ssh_config or not a.ssh_config.is_file():raise ValueError('pinned restricted SSH config required')
    actual=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
    if actual!=approval['source_commit']:raise ValueError('exact approved coordinator commit required')
    if approval.get('client_hashes')!=client_hashes(root):raise ValueError('unchanged qualified workload hashes required')
    source=approval.get('image_source','')
    if len(source)!=40:raise ValueError('immutable application image source required')
    subprocess.run(['git','diff','--exit-code',source,'HEAD','--','internal/','migrations/','cmd/api/','cmd/capacity-lab/'],check=True)
    if hashlib.sha256((root/'deploy/session-lab/sessions.js').read_text().encode()).hexdigest()!='db3013f473b41fc7d35e01d7385d620b247b977e3dd86e9e8683d14a4bb337a3':raise ValueError('original generator proof source changed')
    if int(next(x.split()[1] for x in Path('/proc/meminfo').read_text().splitlines() if x.startswith('MemAvailable:')))*1024<6*2**30:raise ValueError('free runner memory reserve insufficient')
    a.output.mkdir(parents=True,exist_ok=False)
    (a.output/'provenance.json').write_text(json.dumps(dict(commit=actual,client_hashes=client_hashes(root),approval=approval),indent=2))
    runner=Runner(root,a.ssh_config,approval,a.output)
    def interrupted(*unused):raise KeyboardInterrupt('trial interrupted')
    signal.signal(signal.SIGTERM,interrupted);signal.signal(signal.SIGINT,interrupted)
    execute_sequence(runner)
    (a.output/'trial-passed.json').write_text(json.dumps(dict(passed=True,production_capacity_claim=False)))

if __name__=='__main__':main()
