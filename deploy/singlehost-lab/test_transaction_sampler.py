import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import transaction_sampler as sampler


class TransactionSamplerTests(unittest.TestCase):
    def test_finite_timeout_is_inside_immutable_owned_container(self):
        args=sampler.command('a'*64,180)
        self.assertEqual(args[:5],['docker','exec','-i','a'*64,'timeout'])
        self.assertIn('180',args);self.assertIn('ON_ERROR_STOP=1',args)
        for seconds in [0,181,-1,True]:
            with self.assertRaises(ValueError):sampler.command('a'*64,seconds)
        with self.assertRaises(ValueError):sampler.command('postgres',10)

    def test_unowned_database_is_rejected_before_query(self):
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder);base=root/('cohost-'+'b'*16);base.mkdir()
            (base/'manifest.json').write_text(json.dumps(dict(run=base.name,services={'postgres':'a'*64})))
            row=dict(Id='a'*64,Config={'Labels':{'atlxpres.cohost.run':'production'}},State={'Running':True})
            with patch.object(sampler,'ROOT',root),patch('transaction_sampler.os.name','posix'),patch('transaction_sampler.os.geteuid',return_value=0,create=True),patch.object(Path,'stat') as stat,patch('transaction_sampler.run',return_value=json.dumps([row])) as call:
                stat.return_value=type('Info',(),{'st_uid':0,'st_mode':0o700})()
                # Do not mock filesystem reads: a wrong label cannot reach SQL.
                with self.assertRaisesRegex(ValueError,'Unowned'):sampler.target(base)
                self.assertEqual(call.call_count,1)

    def test_query_contains_no_output_of_raw_sql_or_parameters(self):
        self.assertIn('SET default_transaction_read_only=on',sampler.SQL)
        self.assertIn('pg_blocking_pids(pid)',sampler.SQL)
        self.assertIn('statement_phase',sampler.SQL)
        self.assertNotIn('query AS',sampler.SQL)
        self.assertNotIn('pg_cancel_backend',sampler.SQL)
        self.assertNotIn('pg_terminate_backend',sampler.SQL)
