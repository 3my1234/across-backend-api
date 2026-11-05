import subprocess,unittest
from pathlib import Path
from unittest.mock import patch,Mock
from ssh_once import execute

class LocalSshTests(unittest.TestCase):
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
