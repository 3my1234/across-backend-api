import json,tempfile,unittest
from pathlib import Path
from unittest.mock import patch,MagicMock
import operator_setup

class OperatorTests(unittest.TestCase):
    def test_cleanup_removes_only_recorded_run_and_expiring_keys(self):
        with tempfile.TemporaryDirectory() as temporary:
            base=Path(temporary)
            (base/'request.json').write_text(json.dumps(dict(run='cohost-0123456789abcdef',role='main')))
            (base/'manifest.json').write_text(json.dumps(dict(container_ids=['a'*64])))
            (base/'main-database.key').write_text('synthetic test secret')
            # Windows lacks fcntl: mock only its process lock, not action selection.
            with patch.dict('sys.modules',{'fcntl':MagicMock()}),patch.object(operator_setup,'call') as call,patch.object(operator_setup.subprocess,'run') as run:
                operator_setup.clean(base)
                commands=[x.args[0] for x in call.call_args_list]
                self.assertEqual(len(commands),2)
                self.assertIn('--remove-disposable-data',commands[0])
                self.assertEqual(commands[0][2],str(base/'manifest.json'))
                self.assertEqual(commands[1][-1],'--remove')
                units=[x.args[0][-1] for x in run.call_args_list]
                self.assertEqual(units,['cohost0123456789abcdefmainguard.service','cohost0123456789abcdefdbtunnel.service','cohost0123456789abcdefmainanchor.service'])
                self.assertFalse((base/'main-database.key').exists())
                call.reset_mock();run.reset_mock()
                operator_setup.clean(base)
                call.assert_not_called();run.assert_not_called()

    def test_cleanup_refuses_to_mark_success_if_owned_cleanup_fails(self):
        with tempfile.TemporaryDirectory() as temporary:
            base=Path(temporary)
            (base/'request.json').write_text(json.dumps(dict(run='cohost-0123456789abcdef',role='database')))
            (base/'manifest.json').write_text(json.dumps(dict(container_ids=['a'*64])))
            with patch.dict('sys.modules',{'fcntl':MagicMock()}),patch.object(operator_setup,'call',side_effect=RuntimeError('ownership refused')),patch.object(operator_setup.subprocess,'run'):
                with self.assertRaises(RuntimeError):operator_setup.clean(base)
            self.assertFalse((base/'cleanup.complete').exists())

if __name__=='__main__':unittest.main()
