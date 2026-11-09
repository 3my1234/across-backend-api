import json,subprocess,tempfile,unittest
from pathlib import Path
from unittest.mock import Mock,patch
import native
from launch import commands

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
    def test_prepared_fixture_identity_checked_before_session_writes(self):
        r=self.runner();r.approval=dict(run=RUN);r.sql=Mock(return_value='across_db:production');r.service=Mock();r.lab=Mock()
        with self.assertRaisesRegex(ValueError,'wrong disposable'):r.prepare()
        r.service.assert_not_called();r.lab.assert_not_called()
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
