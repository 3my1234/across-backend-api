import json,tempfile,types,unittest
from pathlib import Path
from unittest.mock import patch
import database_patch as subject

class DatabasePatchTests(unittest.TestCase):
    def test_exact_reviewed_sql_and_tampering(self):
        self.assertIn('CREATE OR REPLACE FUNCTION notification_sender_identity',subject.reviewed_sql())
        with patch.object(Path,'read_bytes',return_value=b'not reviewed'):
            with self.assertRaisesRegex(ValueError,'checksum'):subject.reviewed_sql()

    def test_wrong_database_marker_never_mutates(self):
        with tempfile.TemporaryDirectory() as folder:
            base=Path(folder)/('cohost-'+'a'*16);base.mkdir()
            with patch.object(subject,'ROOT',base.parent),patch.object(subject,'os',types.SimpleNamespace(name='posix',geteuid=lambda:0)),patch.object(subject,'sql',return_value='production') as sql:
                with self.assertRaisesRegex(ValueError,'Synthetic DB'):subject.apply(base)
                self.assertEqual(sql.call_count,1)
                self.assertNotIn('BEGIN',sql.call_args.args[1])

    def test_apply_and_verify_actual_body_before_recording(self):
        body=subject.reviewed_sql().split('AS $$',1)[1].rsplit('$$;',1)[0].strip()
        with tempfile.TemporaryDirectory() as folder:
            base=Path(folder)/('cohost-'+'a'*16);base.mkdir()
            with patch.object(subject,'ROOT',base.parent),patch.object(subject,'os',types.SimpleNamespace(name='posix',geteuid=lambda:0)),patch.object(subject,'sql',side_effect=['disposable-compose-only','','disposable-compose-only',body,'1']) as sql:
                report=subject.apply(base)
                self.assertTrue(report['function_verified'])
                self.assertTrue(sql.call_args_list[1].args[1].startswith('BEGIN;'))
                self.assertTrue(sql.call_args_list[1].args[1].endswith('COMMIT;'))
                self.assertEqual(json.loads((base/'database-patch.json').read_text()),report)

    def test_running_function_mismatch_fails(self):
        with patch.object(subject,'require_target'),patch.object(subject,'sql',return_value='different function'):
            with self.assertRaisesRegex(ValueError,'differs'):subject.verify(Path('unused'))

    def test_approval_must_pin_the_exact_patch(self):
        import test_policy
        from policy import validate
        approval=test_policy.PolicyTests().approval()
        approval['database_patch']=subject.metadata();validate(approval,now=100)
        approval['database_patch']={**approval['database_patch'],'sha256':'0'*64}
        with self.assertRaisesRegex(ValueError,'Unreviewed database patch'):validate(approval,now=100)
