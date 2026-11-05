import json,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
from api_artifact import manifest,verify_running,SOURCE

class ApiArtifactTests(unittest.TestCase):
    def test_running_executable_must_match_both_read_only_mounts(self):
        with tempfile.TemporaryDirectory() as folder,patch('verifier_build.validate_binary'):
            base=Path(folder);(base/'api.bin').write_bytes(b'approved synthetic executable')
            expected=manifest(base/'api.bin');(base/'api-artifact.json').write_text(json.dumps(expected))
            rows=[dict(Id=n,Path='/app/api',State={'Running':True},Mounts=[dict(Source=str(base/'api.bin'),Destination='/app/api',RW=False)]) for n in ('one','two')]
            with patch('runtime.inventory',return_value=({'services':{'api1':'one','api2':'two'}},rows)),patch('runtime.call',return_value=(expected['api_sha256']+'  /proc/1/exe').encode()) as call:
                self.assertTrue(verify_running(base,expected)['both_running_apis_verified'])
                self.assertEqual(call.call_count,2)
                for args,kwargs in call.call_args_list:self.assertEqual(args[0][-2:],['sha256sum','/proc/1/exe'])
                rows[1]['Mounts'][0]['RW']=True
                with self.assertRaisesRegex(ValueError,'mount mismatch'):verify_running(base,expected)
                rows[1]['Mounts'][0]['RW']=False
                call.return_value=b'wronghash /proc/1/exe'
                with self.assertRaisesRegex(ValueError,'checksum mismatch'):verify_running(base,expected)
            wrong={**expected,'api_source':'0'*40}
            with self.assertRaisesRegex(ValueError,'artifact identity'):verify_running(base,wrong)
