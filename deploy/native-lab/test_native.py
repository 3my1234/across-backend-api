import json,subprocess,tempfile,unittest
from pathlib import Path
from unittest.mock import Mock,patch
import native
from launch import commands
from trial_policy import STAGES,validate
from control import command
from native_budget import additional_required,shared,SHARED_MEMORY
import time

RUN='cohost-0123456789abcdef'
class NativeTests(unittest.TestCase):
    def runner(self):
        r=object.__new__(native.NativeRunner)
        r.native=dict(network=RUN+'-main_lab',generator_ip='172.29.248.21',generator_image='sha256:'+'a'*64)
        r.base=Path('/root/atlxpres-cohost')/RUN;r.config=r.base/'peer.conf'
        return r
    def test_local_main_and_noninteractive_pinned_database(self):
        r=self.runner();main=r.control_process('main','status');db=r.control_process('database','status')
        self.assertNotIn('ssh',main);self.assertIn('local.py',main[1]);self.assertIn('BatchMode=yes',db)
        self.assertIn('StrictHostKeyChecking=yes',db);self.assertIn('NumberOfPasswordPrompts=0',db)
        self.assertEqual(r.generator_network(),['--network',RUN+'-main_lab','--ip','172.29.248.21'])
        with self.assertRaises(ValueError):r.control_process('production','cleanup')
    def test_systemd_stop_kill_deadline_and_peer_failure_have_cleanup(self):
        prefix,peer,job=commands('/root/test/native.py','/root/test/native.json',RUN)
        self.assertIn('--property=RuntimeMaxSec=7200',job)
        self.assertIn('--property=KillMode=control-group',job)
        self.assertIn('--property=Restart=no',job)
        self.assertIn('--property=BindsTo='+prefix+'-peer.service',job)
        self.assertTrue(any(s.startswith('--property=ExecStopPost=') and 'cleanup' in s for s in job))
        self.assertIn('--property=ExecStopPost=/usr/bin/systemctl --no-block stop '+prefix+'-peer.service',job)
        self.assertNotIn('--scope',job);self.assertNotIn('--wait',job)
        with self.assertRaises(ValueError):commands('/root/test/native.py','/root/test/native.json',RUN,lifetime=7201)
    def test_native_retains_reviewed_split_and_unchanged_workload(self):
        approval=dict(execution_mode='vps-native',timing_policy='http-2s-observer-2.5s-v1',run=RUN,load_authorized=True,fixture_preparation_authorized=True,spending_cap_usd=0,main_cpu=1,main_memory=2684354560,database_cpu=2,database_memory=3221225472,generator_cpu=2,generator_memory=4294967296,shared_main_memory=SHARED_MEMORY,lifetime_seconds=7200,approved_unix=time.time(),source_commit='a'*40,stages=[list(s) for s in STAGES])
        validate(approval,RUN)
        self.assertEqual(STAGES[-1],('sustained-500','sustained',500,1800))
        for key,value in (('main_cpu',2),('database_cpu',3),('generator_memory',2*2**30),('stages',[])):
            with self.subTest(key=key),self.assertRaises(ValueError):validate(dict(approval,**{key:value}),RUN)
    def test_observed_host_fits_shared_budget_and_existing_usage_not_double_counted(self):
        additional_required(6479*2**20,0)
        additional_required((6479-500)*2**20,500*2**20)
        with self.assertRaises(ValueError):additional_required(6300*2**20,0)
        with self.assertRaises(ValueError):shared('production')
    def test_prepared_fixture_identity_checked_before_session_writes(self):
        r=self.runner();r.approval=dict(run=RUN);r.sql=Mock(return_value='across_db:production');r.service=Mock();r.lab=Mock()
        with self.assertRaisesRegex(ValueError,'wrong disposable'):r.prepare()
        r.service.assert_not_called();r.lab.assert_not_called()
    def test_fanout_cannot_start_without_load_approval_and_identity_is_fixed(self):
        manifest=dict(role='main',services={'message-fanout':'a'*64},container_ids=['a'*64],load_authorized=False)
        with self.assertRaisesRegex(ValueError,'not authorized'):command(manifest,dict(action='start',service='message-fanout'))
        self.assertEqual(command(manifest,dict(action='binary-identity',service='message-fanout')),['docker','exec','a'*64,'sha256sum','/proc/1/exe'])
    def test_database_cleanup_precedes_main_key_removal_and_is_idempotent(self):
        with tempfile.TemporaryDirectory() as d:
            base=Path(d);r=Mock(base=base)
            calls=[]
            def ssh(role,*args,**kwargs):
                calls.append(role)
                if role=='main':(base/'cleanup.complete').touch()
            r.ssh=Mock(side_effect=ssh)
            with patch.object(native,'NativeRunner',return_value=r),patch.object(native.subprocess,'run',return_value=Mock(returncode=1)):
                native.cleanup(dict(run=RUN,main_base=d));native.cleanup(dict(run=RUN,main_base=d))
            self.assertEqual(calls,['database','main'])
    def test_foreign_generator_preserved_but_owned_hosts_still_cleaned(self):
        with tempfile.TemporaryDirectory() as d:
            r=Mock(base=Path(d));found=Mock(returncode=0,stdout=json.dumps([dict(Config=dict(Labels={'atlxpres.cohost.run':'other'}))]))
            with patch.object(native,'NativeRunner',return_value=r),patch.object(native.subprocess,'run',return_value=found) as run:
                with self.assertRaisesRegex(RuntimeError,'cleanup incomplete'):native.cleanup(dict(run=RUN,main_base=d))
            self.assertEqual([c.args[0] for c in r.ssh.call_args_list],['database','main'])
            self.assertEqual(run.call_count,1)
    def test_remote_cleanup_failure_remains_visible(self):
        with tempfile.TemporaryDirectory() as d:
            r=Mock(base=Path(d));r.ssh.side_effect=[subprocess.TimeoutExpired('ssh',180),None]
            with patch.object(native,'NativeRunner',return_value=r),patch.object(native.subprocess,'run',return_value=Mock(returncode=1)):
                with self.assertRaises(RuntimeError):native.cleanup(dict(run=RUN,main_base=d))
            report=json.loads((Path(d)/'native-cleanup.json').read_text())
            self.assertIn('unconfirmed',report['hosts']['database']);self.assertEqual(report['hosts']['main'],'confirmed')

if __name__=='__main__':unittest.main()
