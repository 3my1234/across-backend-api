import json,os,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
from api_artifact import manifest,verify_running,SOURCE
from install_host import reuse_api

class ApiArtifactTests(unittest.TestCase):
    @unittest.skipIf(os.name=='nt','Linux cache ownership and hardlinks')
    def test_cached_api_hash_checked_before_link_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as folder,patch('verifier_build.validate_binary'):
            root=Path(folder);old=root/('cohost-'+'a'*16);old.mkdir();base=root/('cohost-'+'b'*16);base.mkdir()
            (old/'api.bin').write_bytes(b'approved cached API');(old/'api.bin').chmod(0o555)
            artifact=manifest(old/'api.bin');(base/'api-artifact.json').write_text(json.dumps(artifact))
            approval={**artifact,'api_cache_run':old.name}
            with self.assertRaisesRegex(ValueError,'checksum'):reuse_api(base,{**approval,'api_sha256':'0'*64})
            self.assertFalse((base/'api.bin').exists())
            reuse_api(base,approval);self.assertEqual((base/'api.bin').stat().st_ino,(old/'api.bin').stat().st_ino)
            with self.assertRaises(FileExistsError):reuse_api(base,approval)
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
