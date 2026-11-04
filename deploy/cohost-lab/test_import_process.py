import io,subprocess,unittest
from unittest.mock import Mock,patch
from pathlib import Path
from import_process import stop_importer,load_archive

class ImportProcessTests(unittest.TestCase):
    def child(self):return Mock(pid=12345,poll=Mock(return_value=None))
    def test_finished_child_is_untouched(self):
        p=self.child();p.poll.return_value=0
        self.assertTrue(stop_importer(p,io.StringIO()));p.terminate.assert_not_called()
    def test_term_then_bounded_kill_on_timeout(self):
        p=self.child();p.wait.side_effect=[subprocess.TimeoutExpired('docker load',3),0]
        log=io.StringIO();self.assertTrue(stop_importer(p,log))
        p.terminate.assert_called_once();p.kill.assert_called_once()
        self.assertEqual(p.wait.call_args_list[0].kwargs,dict(timeout=3))
        self.assertEqual(p.wait.call_args_list[1].kwargs,dict(timeout=3))
        self.assertIn('"import_cleanup": "kill"',log.getvalue())
    def test_cleanup_failure_does_not_mask_original_guard_error(self):
        p=self.child();p.wait.side_effect=subprocess.TimeoutExpired('docker load',3)
        log=io.StringIO()
        with self.assertRaisesRegex(ValueError,'guard lost'):
            try:raise ValueError('guard lost')
            finally:self.assertFalse(stop_importer(p,log))
        self.assertIn('"import_cleanup": "unconfirmed"',log.getvalue())
    def test_native_archive_has_no_blocking_python_stdin_pipe(self):
        p=self.child();p.poll.return_value=0;p.returncode=0
        guard=Mock()
        with patch('import_process.subprocess.Popen',return_value=p) as launch:
            load_archive(Path('/reviewed/images.tar.gz'),io.StringIO(),guard)
        self.assertEqual(launch.call_args.args[0],['docker','load','--input',str(Path('/reviewed/images.tar.gz'))])
        self.assertEqual(launch.call_args.kwargs['stdin'],subprocess.DEVNULL)
        self.assertEqual(guard.call_count,2)
    def test_guard_loss_terminates_started_child_and_preserves_cause(self):
        p=self.child();guard=Mock(side_effect=[None,ValueError('guard lost')])
        with patch('import_process.subprocess.Popen',return_value=p),self.assertRaisesRegex(ValueError,'guard lost'):
            load_archive(Path('/reviewed/images.tar.gz'),io.StringIO(),guard)
        p.terminate.assert_called_once()
