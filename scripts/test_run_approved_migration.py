import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
from types import SimpleNamespace
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('operator', Path(__file__).with_name('run-approved-migration.py'))
operator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(operator)


class MigrationRunner(unittest.TestCase):
    def test_actual_logging_path_is_private_and_does_not_echo_process_output(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)/'private.log'
            def invoke():
                self.assertTrue(path.is_file(), 'log must exist before mutation starts')
                if os.name == 'posix':
                    self.assertEqual(stat.S_IMODE(path.stat().st_mode),0o600)
                return SimpleNamespace(returncode=0,stdout='private-url-fixture',stderr='private-error-fixture')
            captured=io.StringIO()
            with contextlib.redirect_stdout(captured):
                result=operator.logged_migration(path,invoke)
            self.assertEqual(result.returncode,0)
            self.assertEqual(captured.getvalue(),'')
            self.assertEqual(path.read_text(encoding='utf-8'),'private-url-fixture\nprivate-error-fixture')

    def test_existing_or_uncreatable_log_prevents_migrator_start(self):
        with tempfile.TemporaryDirectory() as directory:
            path=Path(directory)/'existing.log'
            path.write_text('retained')
            invoke=Mock(side_effect=AssertionError('mutator must not run'))
            with self.assertRaises(FileExistsError):
                operator.logged_migration(path,invoke)
            invoke.assert_not_called()
            self.assertEqual(path.read_text(),'retained')
            with self.assertRaises(FileNotFoundError):
                operator.logged_migration(Path(directory)/'missing'/'private.log',invoke)
            invoke.assert_not_called()

    def test_only_fixed_private_database_is_allowed(self):
        good = {'APP_ENV':'production','DB_VERIFIED_VPN_HOST':'10.77.0.2','MIGRATION_DATABASE_URL':'postgres://synthetic:synthetic@10.77.0.2:5432/across_db?sslmode=disable'}
        operator.validate_target(good)
        for raw in ['postgres://127.0.0.1/across_db','postgres://10.77.0.2/other_db','postgres://10.77.0.2/across_db?host=other','postgres://79.143.176.173/across_db']:
            with self.assertRaises(ValueError):
                operator.validate_target(dict(good,MIGRATION_DATABASE_URL=raw))

    def test_preview_cannot_launch_external_process(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root/'migrations').mkdir()
            for name in ['across-config-check','across-migrate','migrations/103_synthetic.sql']:
                (root/name).write_text('synthetic-public-artifact')
            files = {p.relative_to(root).as_posix():hashlib.sha256(p.read_bytes()).hexdigest() for p in root.rglob('*') if p.is_file()}
            revision = 'a'*40
            (root/'manifest.json').write_text(json.dumps({'revision':revision,'platform':'linux/amd64','files':files,'migration_count':1}))
            args = ['operator','--toolkit',str(root),'--revision',revision,'--runtime-image','sha256:'+'b'*64,'--env-file',str(root/'candidate-private.env')]
            out = io.StringIO()
            with patch.object(sys,'argv',args), patch.object(operator,'private_env',return_value={}), patch.object(operator.subprocess,'run',side_effect=AssertionError('Preview launched a process')), contextlib.redirect_stdout(out):
                operator.main()
            self.assertTrue(json.loads(out.getvalue())['preview_only'])
            (root/'across-migrate').write_text('modified')
            with self.assertRaisesRegex(ValueError,'checksum'):
                operator.inventory(root,revision)

    def test_unlisted_migration_or_path_escape_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root/'manifest.json').write_text(json.dumps({'revision':'a'*40,'platform':'linux/amd64','files':{'../private':'a'*64,'across-migrate':'b'*64,'across-config-check':'c'*64},'migration_count':0}))
            with self.assertRaisesRegex(ValueError,'Unexpected'):
                operator.inventory(root,'a'*40)

    def test_migrator_has_no_api_worker_ports_or_privileges(self):
        args = operator.command('sha256:'+'a'*64,Path('/root/toolkit'),'owned-name','owner',Path('/root/private.env'),'coolify',True)
        self.assertIn('--read-only',args)
        self.assertIn('--pull=never',args)
        self.assertIn('10001:10001',args)
        self.assertIn('0.5',args)
        self.assertNotIn('--publish',args)
        self.assertNotIn('/app/across-api',args)
        self.assertNotIn('/app/across-worker',args)
        self.assertIn('/candidate/across-migrate',args)


if __name__ == '__main__':
    unittest.main()
