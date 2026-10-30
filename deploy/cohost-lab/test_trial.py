import io,json,tempfile,time,unittest
from pathlib import Path
from unittest.mock import patch
from trial_policy import validate,STAGES,LIFETIME
from exchange import put,get,path_for

class TrialPolicyTests(unittest.TestCase):
    def approval(self):
        return dict(timing_policy='http-2s-observer-2.5s-v1',run='cohost-'+'a'*16,load_authorized=True,fixture_preparation_authorized=True,spending_cap_usd=0,
            main_cpu=2,main_memory=2684354560,database_cpu=3,database_memory=3221225472,
            generator_cpu=2,generator_memory=4294967296,lifetime_seconds=LIFETIME,
            approved_unix=time.time(),source_commit='a'*40,stages=[list(s) for s in STAGES])
    def test_exact_envelope_and_no_1000(self):
        a=self.approval();validate(a,a['run']);self.assertEqual(STAGES[-1],('sustained-500','sustained',500,1800))
        self.assertEqual(max(s[2] for s in STAGES),500)
    def test_changed_caps_stages_false_approval_old_or_future_rejected(self):
        for key,value in [('load_authorized',False),('fixture_preparation_authorized',False),('main_cpu',3),('database_cpu',4),('spending_cap_usd',1),('lifetime_seconds',99999),('stages',[]),('approved_unix',0),('approved_unix',time.time()+100),('source_commit','main')]:
            a=self.approval();a[key]=value
            with self.subTest(key=key),self.assertRaises(ValueError):validate(a,a['run'])
    def test_plan_never_starts_process(self):
        import trial_runner,sys
        with patch.object(sys,'argv',['trial_runner.py','--plan']),patch('trial_runner.subprocess.Popen') as process,patch('trial_runner.subprocess.check_output') as output,patch('builtins.print'):
            trial_runner.main()
        process.assert_not_called();output.assert_not_called()

class ExchangeTests(unittest.TestCase):
    def fixture(self,path,load=True):
        (path/'exchange').mkdir();(path/'manifest.json').write_text(json.dumps(dict(role='main',load_authorized=load)))
    def test_allowlist_blocks_paths_private_keys_and_fixture_upload(self):
        for name in ('../manifest.json','main-database.key','fixtures.json','mock-key.pem','approval.json'):
            with self.assertRaises(ValueError):path_for(Path('.'),name,True)
    def test_evidence_uploaded_atomically_and_only_with_load_approval(self):
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp);self.fixture(base)
            put(base,'summary.json',io.BytesIO(b'{"run_id":"synthetic"}'))
            self.assertEqual(json.loads((base/'exchange/summary.json').read_text())['run_id'],'synthetic')
            (base/'manifest.json').write_text('{"role":"main","load_authorized":false}')
            with self.assertRaises(ValueError):put(base,'k6.log',io.BytesIO(b'ACK:test'))
    def test_invalid_json_and_oversize_never_replace_existing_evidence(self):
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp);self.fixture(base)
            p=base/'exchange/summary.json';p.write_text('original')
            with self.assertRaises(ValueError):put(base,'summary.json',io.BytesIO(b'bad'))
            self.assertEqual(p.read_text(),'original')
            with self.assertRaises(ValueError):put(base,'k6-exit-code.txt',io.BytesIO(b'x'*17))
            self.assertFalse((base/'exchange/k6-exit-code.txt').exists())
    def test_timeout_code_and_scoped_download(self):
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp);self.fixture(base)
            put(base,'k6-exit-code.txt',io.BytesIO(b'99'))
            (base/'exchange/fixtures.json').write_bytes(b'private synthetic fixture')
            target=io.BytesIO();get(base,'fixtures.json',target)
            self.assertEqual(target.getvalue(),b'private synthetic fixture')


class PeerProgressTests(unittest.TestCase):
    def test_repeat_is_not_lease_proof_and_stale_still_aborts(self):
        from trial_policy import peer_progress
        run='cohost-'+'a'*16
        rows=[dict(run=run,role=r,age_seconds=1,sequence=10) for r in ('main','database')]
        self.assertIsNone(peer_progress(rows,dict(main=10,database=10),run))
        rows[0]['sequence']=11
        self.assertIsNone(peer_progress(rows,dict(main=10,database=10),run))
        rows[1]['age_seconds']=3.01
        with self.assertRaises(ValueError):peer_progress(rows,dict(main=10,database=10),run)
    def test_both_must_advance_without_regression(self):
        from trial_policy import peer_progress
        run='cohost-'+'a'*16
        rows=[dict(run=run,role=r,age_seconds=1,sequence=11) for r in ('main','database')]
        self.assertEqual(peer_progress(rows,dict(main=10,database=10),run),dict(main=11,database=11))
        rows[0]['sequence']=9
        with self.assertRaises(ValueError):peer_progress(rows,dict(main=10,database=10),run)


class SequenceTests(unittest.TestCase):
    def test_full_stage_order_workers_only_final_and_cleanup(self):
        from unittest.mock import MagicMock
        from trial_runner import execute_sequence
        runner=MagicMock()
        with patch('trial_runner.time.sleep'):execute_sequence(runner)
        self.assertEqual([c.args[0] for c in runner.measure.call_args_list],list(STAGES))
        runner.service.assert_any_call('notification-provider');runner.service.assert_any_call('notification-worker')
        runner.notifications.assert_called_once();runner.cleanup.assert_called_once()
    def test_failure_or_interruption_stops_escalation_and_always_cleans(self):
        from unittest.mock import MagicMock
        from trial_runner import execute_sequence
        for error in (RuntimeError('gate failed'),KeyboardInterrupt('cancelled')):
            runner=MagicMock();runner.measure.side_effect=[None,error]
            with self.subTest(error=error),self.assertRaises(type(error)):
                execute_sequence(runner)
            self.assertEqual(runner.measure.call_count,2)
            runner.notifications.assert_not_called();runner.service.assert_not_called();runner.cleanup.assert_called_once()
    def test_cleanup_failure_cannot_be_success(self):
        from unittest.mock import MagicMock
        from trial_runner import execute_sequence
        runner=MagicMock();runner.cleanup.side_effect=RuntimeError('cleanup missing')
        with patch('trial_runner.time.sleep'),self.assertRaises(RuntimeError):execute_sequence(runner)

class StartupGateTests(unittest.TestCase):
    def test_mock_provider_cannot_start_under_setup_approval(self):
        from control import command
        m=dict(role='main',services={'notification-provider':'a'*64},container_ids=['a'*64],load_authorized=False)
        with self.assertRaises(ValueError):command(m,dict(action='start',service='notification-provider'))


class ActualGeneratorTests(unittest.TestCase):
    def samples(self,fraction=.2,memory=100000000):
        return [dict(time_unix=float(i),cpu_max='200000 100000',cpu=dict(usage_usec=int(i*1000000*2*fraction),nr_periods=i*10,nr_throttled=0),memory_current=memory,memory_events=dict(oom=0,oom_kill=0)) for i in range(21)]
    def test_original_cpu_memory_and_coverage_limits(self):
        from generator_monitor import resource_gate
        self.assertTrue(resource_gate(self.samples(),0,20,20)['passed'])
        self.assertFalse(resource_gate(self.samples(.71),0,20,20)['passed'])
        self.assertFalse(resource_gate(self.samples(memory=3*2**30),0,20,20)['passed'])
        self.assertFalse(resource_gate(self.samples(),0,20,1800)['passed'])
    def test_original_throttle_and_oom_limits(self):
        from generator_monitor import resource_gate
        rows=self.samples()
        for row in rows:row['cpu']['nr_throttled']=int(row['time_unix'])
        self.assertFalse(resource_gate(rows,0,20,20)['passed'])
        rows=self.samples();rows[-1]['memory_events']['oom_kill']=1
        self.assertFalse(resource_gate(rows,0,20,20)['passed'])
    def test_wrong_actual_quota_refused(self):
        from generator_monitor import resource_gate
        rows=self.samples();rows[-1]['cpu_max']='100000 100000'
        with self.assertRaises(ValueError):resource_gate(rows,0,20,20)


class ObserverTransitionTests(unittest.TestCase):
    def test_valid_approval_required_without_restarting_observer(self):
        from watchdog import approved_trial_transition
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp);a=TrialPolicyTests().approval()
            (base/'trial-approval.json').write_text(json.dumps(a));(base/'trial-started').write_text('started')
            current=dict(run=a['run'],fixture_preparation_authorized=True,trial_deadline_unix=time.time()+7100)
            with patch('watchdog.Path.stat',return_value=SimpleNamespace(st_uid=0,st_mode=0o600)):
                approved_trial_transition(base,current)
                a['load_authorized']=False;(base/'trial-approval.json').write_text(json.dumps(a))
                with self.assertRaises(ValueError):approved_trial_transition(base,current)
    def test_missing_root_approval_never_promotes_setup_mode(self):
        from watchdog import approved_trial_transition
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(FileNotFoundError):approved_trial_transition(Path(tmp),dict(run='cohost-'+'a'*16))


class RestrictedSetupVerifyTests(unittest.TestCase):
    def test_only_exact_main_setup_verification(self):
        from access import setup_verify_command
        run='cohost-'+'a'*16;base=Path('/root/atlxpres-cohost')/run
        manifest=dict(run=run,role='main',load_authorized=False)
        command=setup_verify_command(base,manifest)
        self.assertEqual(command[-1],'verify');self.assertEqual(command[1],str(base/'tools/operator_setup.py'))
        for changed in (dict(manifest,role='database'),dict(manifest,load_authorized=True),dict(manifest,run='production')):
            with self.assertRaises(ValueError):setup_verify_command(base,changed)
        with self.assertRaises(ValueError):setup_verify_command(Path('/tmp'),manifest)
