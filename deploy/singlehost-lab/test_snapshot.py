import copy,json,os,tempfile,time,unittest
from pathlib import Path
from unittest.mock import patch
import snapshot

class SnapshotTests(unittest.TestCase):
    def test_complete_multiline_postgres_json_inventory_is_parsed(self):
        expected={'tables':{'one':{'rows':2}},'triggers':[{'tgname':'first'},{'tgname':'second'}]}
        with patch('snapshot.sql',return_value=json.dumps(expected,indent=2)):
            self.assertEqual(snapshot.database_state(Path('unused')),expected)
    def fixture(self,root):
        p=root/'fixture.dump';p.write_bytes(b'PGDMPsynthetic-test-only')
        rows={'capacity_lab_actors':500,'users':1000,'products':5000,'provider_listings':5000,'chat_rooms':100050,'chat_messages':1025000,'provider_conversation_messages':500000,'schema_migrations':108}
        return p,dict(image_source=snapshot.APP_SOURCE,scenario='large-busy',actor_pairs=500,created_unix=100,sha256=snapshot.digest(p),bytes=p.stat().st_size,database={'tables':{n:{'rows':v} for n,v in rows.items()}})
    def test_corruption_wrong_source_expiry_and_reduced_fixture_are_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            p,m=self.fixture(Path(folder));snapshot.validate(p,m,now=100)
            for field,value in [('image_source','0'*40),('actor_pairs',10),('sha256','0'*64),('bytes',1),('created_unix',-86400)]:
                with self.subTest(field=field),self.assertRaises(ValueError):snapshot.validate(p,{**m,field:value},now=100)
            reduced=copy.deepcopy(m);reduced['database']['tables']['chat_messages']['rows']=20000
            with self.assertRaises(ValueError):snapshot.validate(p,reduced,now=100)
            p.write_bytes(b'PGDMPdifferent-test-only')
            with self.assertRaises(ValueError):snapshot.validate(p,m,now=100)
    def test_existing_database_and_wrong_cluster_never_start_restore(self):
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder);p,m=self.fixture(root);m['created_unix']=time.time()
            for responses in [['capacity_lab:wrong'],['capacity_lab:cohost-'+'a'*16,'1']]:
                with patch('snapshot.postgres',return_value=({'run':'cohost-'+'a'*16},'immutable-id')),patch('snapshot.sql',side_effect=responses),patch('snapshot.subprocess.Popen') as restore:
                    with self.assertRaises(ValueError):snapshot.restore(root,p,m)
                    restore.assert_not_called()
    @unittest.skipIf(os.name=='nt','Unix cache ownership/modes are exercised in Linux CI')
    def test_cached_fixture_is_verified_and_linked_without_overwriting(self):
        from install_host import reuse_fixture
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder).resolve();cache=root/('cohost-'+'a'*16);cache.mkdir();p,m=self.fixture(cache);p.chmod(0o600);m['created_unix']=time.time()
            base=root/('cohost-'+'b'*16);base.mkdir();(base/'fixture.json').write_text(json.dumps(m))
            approval={'snapshot_cache_run':cache.name,'snapshot_sha256':m['sha256']}
            reuse_fixture(base,approval)
            self.assertEqual((base/'fixture.dump').read_bytes(),p.read_bytes())
            with self.assertRaises(FileExistsError):reuse_fixture(base,approval)
            with self.assertRaises(ValueError):reuse_fixture(base,{**approval,'snapshot_sha256':'0'*64})

if __name__=='__main__':unittest.main()
