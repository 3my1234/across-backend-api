import base64,hashlib,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
import preflight_host

class PreUploadTests(unittest.TestCase):
    def test_preflight_rejects_existing_resources_and_source_mismatch(self):
        cache='cohost-'+'a'*16;run='cohost-'+'b'*16
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder);base=root/cache;base.mkdir();(base/'cleanup.finalized').touch()
            tools=base/'src/deploy/cohost-lab';tools.mkdir(parents=True)
            (tools/'watchdog.py').write_text('known observer')
            with patch.object(preflight_host,'ROOT',root),patch('preflight_host.os.geteuid',return_value=0,create=True),patch('preflight_host.subprocess.check_output',return_value='container'):
                with self.assertRaisesRegex(ValueError,'containers remain'):preflight_host.preflight(run,cache,'',{})
            with patch.object(preflight_host,'ROOT',root),patch('preflight_host.os.geteuid',return_value=0,create=True),patch('preflight_host.subprocess.check_output',return_value=''):
                with self.assertRaisesRegex(ValueError,'observer source mismatch'):preflight_host.preflight(run,cache,'',{'watchdog.py':'0'*64})
    def test_baseline_runs_before_any_container_start_with_hard_deadline(self):
        cache='cohost-'+'a'*16;run='cohost-'+'b'*16
        code=base64.b64encode(b'def main():\n import sys\n from pathlib import Path\n Path(sys.argv[1]).write_text("synthetic unit evidence")\n').decode()
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder);base=root/cache;base.mkdir();(base/'cleanup.finalized').touch()
            old_argv=preflight_host.sys.argv[:];old_path=preflight_host.sys.path[:]
            try:
                with patch.object(preflight_host,'ROOT',root),patch('preflight_host.os.geteuid',return_value=0,create=True),patch('preflight_host.subprocess.check_output',return_value='') as command,patch('preflight_host.signal.signal'),patch('preflight_host.signal.SIGALRM',14,create=True),patch('preflight_host.signal.alarm',create=True) as alarm:
                    preflight_host.preflight(run,cache,code,{})
                    self.assertEqual(alarm.call_args_list[0].args,(420,));self.assertEqual(alarm.call_args_list[-1].args,(0,))
                    self.assertTrue((base/('preflight-'+run)/'baseline.json').exists())
                    self.assertEqual(command.call_count,1);self.assertIn('ps',command.call_args.args[0]);self.assertNotIn('start',command.call_args.args[0])
            finally:preflight_host.sys.argv=old_argv;preflight_host.sys.path[:]=old_path
