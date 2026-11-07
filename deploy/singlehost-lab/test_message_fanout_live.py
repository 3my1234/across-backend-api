import copy,json,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
from message_fanout_live import metadata,validate,validate_files,finish
from install_host import extract
import tarfile,io

class LiveFanoutTests(unittest.TestCase):
    def test_immutable_worker_migration_and_caps(self):
        approval={'message_fanout':metadata()};validate(approval)
        for key,value in [('worker_sha256','0'*64),('migration',109),('worker_max_connections',8),('completion_lag_seconds',100),('mode','inline'),('isolated_only',False)]:
            other=copy.deepcopy(approval);other['message_fanout'][key]=value
            with self.assertRaises(ValueError):validate(other)
        with self.assertRaises(ValueError):validate({})

    def test_wrong_worker_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp);(base/'worker.bin').write_bytes(b'wrong')
            with patch('verifier_build.validate_binary'):
                with self.assertRaisesRegex(ValueError,'executable mismatch'):validate_files(base)

    def test_worker_extract_executable_and_not_tiny_file_limit(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);archive=root/'package.tar';target=root/'out';target.mkdir()
            with tarfile.open(archive,'w') as out:
                row=tarfile.TarInfo('worker.bin');row.size=3*2**20;out.addfile(row,io.BytesIO(b'x'*row.size))
            extract(archive,target)
            self.assertEqual((target/'worker.bin').stat().st_size,3*2**20)

    def test_failed_ledger_is_saved_before_rejection(self):
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp);approval={'message_fanout':metadata()}
            observed={'failed':1,'pending':1,'jobs':1,'max_completed_lag_seconds':11}
            with patch('message_fanout_live.require_target'),patch('message_fanout_live.observe',return_value=observed),patch('message_fanout_live.sql',return_value='{}'):
                with self.assertRaises(ValueError):finish(base,approval)
            self.assertEqual(json.loads((base/'message-fanout-ledger.json').read_text())['failed'],1)
