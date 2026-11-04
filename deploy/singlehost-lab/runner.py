"""Single-host adapter for the unchanged qualified workload and reconciliation."""
import argparse,json,os,signal,subprocess,sys,threading,time
from pathlib import Path
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'cohost-lab'))
from trial_runner import Runner,execute_sequence
from trial_policy import STAGES,client_hashes

class SingleHostRunner(Runner):
    def progress(self,label,operation):
        began=time.monotonic();done=threading.Event()
        def report():
            while not done.wait(30):print(json.dumps(dict(phase=label,elapsed_seconds=int(time.monotonic()-began),state='in progress; not a passing result')),flush=True)
        heartbeat=threading.Thread(target=report,daemon=True);heartbeat.start()
        print(json.dumps(dict(phase=label,state='started')),flush=True)
        try:return operation()
        finally:done.set();heartbeat.join(timeout=1)

    def measure(self,stage):
        self.progress('stage '+stage[0]+' (planned '+str(stage[2])+' sessions)',lambda:super(SingleHostRunner,self).measure(stage))
        summary=json.loads((self.output/stage[0]/'summary.json').read_text());metrics=summary['metrics']
        values=lambda name:metrics.get(name,{}).get('values',{})
        print(json.dumps(dict(stage=stage[0],all_original_stage_gates_passed=True,planned_sessions=stage[2],
            observed_peak_vus=values('vus').get('max'),session_starts=values('session_starts').get('count'),
            k6_http_requests_per_second=values('http_reqs').get('rate'),unexpected_error_rate=values('unexpected_failure').get('rate'),
            successful_latency_ms=values('successful_latency'))),flush=True)

    def notifications(self):
        self.progress('durable notification drain and delayed receipt verification',lambda:super(SingleHostRunner,self).notifications())

    def prepare(self):
        for name in ('seed','exporter','verifier','lifecycle','queue-check'):self.service(name)
        self.progress('synthetic500-pair seed; no application load yet',lambda:self.lab('seed','seed',self.output/'seed.log'))
        print('Seed completed; preparing private session fixtures (tokens withheld).',flush=True)
        self.lab('prepare-sessions')
        self.lab('notification-prepare',destination=self.output/'notification-preparation.log')
        self.sql('CREATE EXTENSION IF NOT EXISTS pg_stat_statements; ANALYZE;')
        for name in ('api1','api2','gateway'):self.service(name)
        # One HTTP-only forward into this host's own private Docker network.
        # No database tunnel, host port mapping or gateway relay process.
        tunnel=subprocess.Popen(['ssh','-F',str(self.config),'-N','-L','127.0.0.1:8080:172.29.248.10:8080','cohost-main'],start_new_session=True)
        self.children.append(tunnel);deadline=time.monotonic()+60
        import urllib.request
        opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
        while True:
            self.check()
            try:
                with opener.open('http://127.0.0.1:8080/api/v1/ready',timeout=3) as response:
                    if json.load(response).get('ok') is True:break
            except Exception:
                if time.monotonic()>deadline:raise
                time.sleep(1)
        self.lab('lifecycle','lifecycle',self.output/'lifecycle.jsonl')
        from queue_probe import exercise as queue_probe
        queue_probe(self.sql,lambda:self.lab('queue-check','queue-check',self.output/'queue-check.jsonl'))
        print('API readiness, lifecycle and isolated queue-recovery preflight passed. Starting staged application traffic.',flush=True)

    def start(self):
        status=json.loads(self.ssh('main','status',timeout=15))
        self.validate_status(status)
        self.ssh('main','activate',timeout=15)
        self.activation_done=True
        def check_local_guard():
            try:
                while not self.stop.wait(2):self.validate_status(json.loads(self.ssh('main','status',timeout=10)))
            except BaseException as error:
                self.error=RuntimeError('single-host observer unavailable: '+str(error))
                # Stop the generator only; server watchdog and hard-stop job stay independent.
                subprocess.run(['docker','stop','--timeout','2','cohost-generator'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
        self.children_thread=threading.Thread(target=check_local_guard,daemon=True);self.children_thread.start()

    def validate_status(self,status):
        if status.get('run')!=self.approval['run'] or status.get('topology')!='singlehost' or status.get('ready') is not True or status.get('closed') or status.get('guard_error'):raise ValueError('wrong/closed single-host trial status')
        age=status.get('age_seconds')
        if type(age) not in (float,int) or not 0<=age<=5:raise ValueError('local watchdog not fresh')

    def telemetry(self,public,stop):
        try:
            while not stop.is_set():
                raw=self.ssh('main','observations',timeout=30)
                with (public/'singlehost-resources.jsonl').open('ab') as out:out.write(raw.rstrip()+b'\n')
                with (public/'database-activity.jsonl').open('a') as out:out.write(self.sql("SELECT json_build_object('time',clock_timestamp(),'clients',count(*),'active',count(*) FILTER(WHERE state='active'),'lock_waiters',count(*) FILTER(WHERE wait_event_type='Lock')) FROM pg_stat_activity WHERE backend_type='client backend';")+'\n')
                with (public/'queue-age.jsonl').open('a') as out:out.write(self.sql("SELECT json_build_object('time',clock_timestamp(),'email_pending',(SELECT count(*) FROM email_outbox WHERE status IN('pending','retry','sending')),'push_pending',(SELECT count(*) FROM notification_push_deliveries WHERE status IN('pending','retry','sending')));")+'\n')
                stop.wait(5)
        except BaseException as error:self.error=RuntimeError('single-host telemetry: '+str(error))

    def cleanup(self):
        try:self.ssh('main','observations-full',destination=self.output/'final-observations.json')
        except Exception as error:(self.output/'observation-error.txt').write_text(str(error))
        self.stop.set()
        for process in self.children:
            if process.poll() is None:
                try:os.killpg(process.pid,signal.SIGTERM)
                except ProcessLookupError:pass
        subprocess.run(['docker','stop','--timeout','2','cohost-generator'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
        self.ssh('main','cleanup',timeout=180,destination=self.output/'host-cleanup.log')

def main():
    p=argparse.ArgumentParser();p.add_argument('--execute',action='store_true');p.add_argument('--approval',type=Path);p.add_argument('--ssh-config',type=Path);p.add_argument('--output',type=Path,default=Path('singlehost-evidence'));a=p.parse_args()
    if not a.execute:
        print(json.dumps(dict(topology='singlehost',stages=STAGES,application_seconds=sum(s[3] for s in STAGES),load_started=False)));return
    if os.environ.get('GITHUB_ACTIONS')!='true' or os.name!='posix':raise ValueError('qualified free Linux generator only')
    approval=json.loads(a.approval.read_text())
    # Same reviewed caps and source fields as host, validated without contacting a VPS.
    from policy import validate
    validate(approval)
    root=Path(__file__).resolve().parents[2]
    commit=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
    if commit!=approval['source_commit'] or approval.get('client_hashes')!=client_hashes(root):raise ValueError('approved source/workload mismatch')
    from provenance import verify
    verify(root)
    subprocess.run(['python3',str(root/'deploy/cohost-lab/check_generator.py'),str(root/'deploy/cohost-lab/proofs/generator-40866958782')],check=True)
    if not a.ssh_config or not a.ssh_config.is_file():raise ValueError('pinned restricted SSH configuration required')
    if int(next(x.split()[1] for x in Path('/proc/meminfo').read_text().splitlines() if x.startswith('MemAvailable:')))*1024<6*2**30:raise ValueError('generator memory reserve insufficient')
    a.output.mkdir(exist_ok=False)
    (a.output/'provenance.json').write_text(json.dumps(dict(source=commit,approval=approval),indent=2))
    runner=SingleHostRunner(root,a.ssh_config,approval,a.output)
    def interrupted(*unused):raise KeyboardInterrupt('trial interrupted')
    signal.signal(signal.SIGTERM,interrupted);signal.signal(signal.SIGINT,interrupted)
    execute_sequence(runner)
    (a.output/'trial-passed.json').write_text(json.dumps(dict(passed=True,topology='singlehost',production_capacity_claim=False)))

if __name__=='__main__':main()
