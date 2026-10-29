import hashlib,importlib.util,io,json,tarfile,tempfile,unittest
from pathlib import Path
spec=importlib.util.spec_from_file_location('complete_staged_package',Path(__file__).resolve().parents[2]/'docs/qualification/complete_staged_package.py')
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)

class StagedPackageTests(unittest.TestCase):
    def fixture(self,base):
        run='cohost-'+'a'*16
        files={'request.json':json.dumps(dict(run=run,role='database')).encode(),
               'approval.json':json.dumps(dict(run=run,authorized_setup=True,load_authorized=False)).encode(),
               'compose.json':b'{}','tools/example.py':b'print(1)'}
        with tarfile.open(base/'setup.tar','w') as stream:
            for name,data in files.items():
                item=tarfile.TarInfo(name);item.size=len(data);stream.addfile(item,io.BytesIO(data))
        return run,{name:hashlib.sha256(data).hexdigest() for name,data in files.items()}
    def test_uploaded_and_already_extracted_packages_both_verify(self):
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp);run,expected=self.fixture(base)
            self.assertEqual(module.complete(base,run,'database',expected)['staged_package'],'verified')
            self.assertFalse((base/'setup.tar').exists())
            self.assertEqual(module.complete(base,run,'database',expected)['staged_package'],'verified')
    def test_setup_manifest_and_mismatched_files_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp);run,expected=self.fixture(base)
            (base/'manifest.json').write_text('{}')
            with self.assertRaisesRegex(ValueError,'already started'):module.complete(base,run,'database',expected)
            (base/'manifest.json').unlink();expected['compose.json']='0'*64
            with self.assertRaisesRegex(ValueError,'file mismatch'):module.complete(base,run,'database',expected)
    def test_archive_traversal_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp);run,expected=self.fixture(base)
            with tarfile.open(base/'setup.tar','w') as stream:
                item=tarfile.TarInfo('../escape');item.size=1;stream.addfile(item,io.BytesIO(b'x'))
            with self.assertRaisesRegex(ValueError,'unsafe'):module.complete(base,run,'database',expected)

if __name__=='__main__':unittest.main()
