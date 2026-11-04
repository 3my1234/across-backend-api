import io,tarfile,tempfile,unittest
from pathlib import Path
from install_host import extract,key_line

class InstallSafetyTests(unittest.TestCase):
    def test_archive_traversal_and_links_rejected(self):
        for name,kind in (('../escape',tarfile.REGTYPE),('/absolute',tarfile.REGTYPE),('link',tarfile.SYMTYPE)):
            with self.subTest(name=name),tempfile.TemporaryDirectory() as folder:
                root=Path(folder);archive=root/'package.tar';base=root/'run';base.mkdir()
                with tarfile.open(archive,'w') as out:
                    entry=tarfile.TarInfo(name);entry.type=kind;entry.size=0;entry.linkname='/etc/passwd' if kind==tarfile.SYMTYPE else '';out.addfile(entry,io.BytesIO())
                with self.assertRaises(ValueError):extract(archive,base)
                self.assertEqual(list(base.iterdir()),[])
    def test_existing_file_cannot_be_overwritten(self):
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder);archive=root/'package.tar';base=root/'run';base.mkdir();(base/'approval.json').write_text('keep')
            with tarfile.open(archive,'w') as out:
                entry=tarfile.TarInfo('approval.json');entry.size=3;out.addfile(entry,io.BytesIO(b'new'))
            with self.assertRaises(FileExistsError):extract(archive,base)
            self.assertEqual((base/'approval.json').read_text(),'keep')
    def test_key_is_forced_expiring_and_only_gateway_forward_is_allowed(self):
        run='cohost-'+'a'*16;base=Path('/root/atlxpres-singlehost')/run
        line=key_line(base,'ssh-ed25519 AAAA synthetic-only','20261104235959Z')
        self.assertIn('restrict,port-forwarding,permitopen="127.0.0.1:58080"',line)
        self.assertIn('expiry-time="20261104235959Z"',line)
        self.assertIn('supervise.py access',line)
        self.assertNotIn('5432',line)
        with self.assertRaises(ValueError):key_line(base,'ssh-rsa AAAA wrong-type','20261104235959Z')

if __name__=='__main__':unittest.main()
