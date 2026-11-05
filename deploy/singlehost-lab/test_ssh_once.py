import json,subprocess,tempfile,unittest
from pathlib import Path
from unittest.mock import patch,Mock
from ssh_once import execute,observe

class LocalSshTests(unittest.TestCase):
    def test_rejected_key_reason_is_saved_and_not_retried(self):
        with tempfile.TemporaryDirectory() as folder,patch('ssh_once.subprocess.run',side_effect=subprocess.CalledProcessError(255,'ssh',stderr=b'Permission denied (publickey,password).')) as call:
            private=Path(folder)
            with self.assertRaises(subprocess.CalledProcessError):observe(private,'setup-status')
            result=json.loads((private/'last-ssh-error.json').read_text())
            self.assertEqual(result['kind'],'authentication_denied');self.assertEqual(result['command'],'setup-status');self.assertEqual(call.call_count,1)
    def test_two_setup_timeouts_are_unknown_then_fresh_ready_can_resume(self):
        with patch('ssh_once.subprocess.run',side_effect=[subprocess.TimeoutExpired('ssh',45),subprocess.TimeoutExpired('ssh',45),Mock(stdout=b'{"ready":true}')]) as call:
            status=json.loads(observe(Path('private'),'setup-status'))
            self.assertFalse(status['observation_available']);self.assertFalse(status['ready'])
            self.assertIsNone(status['closed']);self.assertIsNone(status['guard_error'])
            self.assertEqual(observe(Path('private'),'setup-status'),b'{"ready":true}')
            self.assertTrue(all(args[0][-1]=='setup-status' for args,kw in call.call_args_list))
    def test_authentication_failure_is_not_hidden(self):
        with patch('ssh_once.subprocess.run',side_effect=subprocess.CalledProcessError(255,'ssh',stderr=b'Permission denied')):
            with self.assertRaises(subprocess.CalledProcessError):observe(Path('private'),'setup-status')
    def test_active_trial_status_timeout_remains_fatal(self):
        with patch('ssh_once.subprocess.run',side_effect=subprocess.TimeoutExpired('ssh',45)):
            with self.assertRaises(subprocess.TimeoutExpired):observe(Path('private'),'status')
    def test_stalled_read_only_status_reconnects_once_without_setup(self):
        with patch('ssh_once.subprocess.run',side_effect=[subprocess.TimeoutExpired('ssh',45),Mock(stdout=b'{"ready":true}')]) as call:
            self.assertEqual(execute(Path('private'),'setup-status'),b'{"ready":true}')
            self.assertEqual(call.call_count,2)
            for args,kw in call.call_args_list:
                self.assertEqual(args[0][-1],'setup-status');self.assertEqual(kw['timeout'],45)
    def test_cleanup_is_never_automatically_repeated(self):
        with patch('ssh_once.subprocess.run',side_effect=subprocess.TimeoutExpired('ssh',180)) as call:
            with self.assertRaises(subprocess.TimeoutExpired):execute(Path('private'),'cleanup')
            self.assertEqual(call.call_count,1)
