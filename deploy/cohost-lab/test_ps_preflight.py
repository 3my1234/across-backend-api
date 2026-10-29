"""Execute the actual PowerShell preflight's shell logic in disposable directories."""
import hashlib,os,subprocess,tempfile,unittest
from pathlib import Path

OLD='/root/atlxpres-cohost/cohost-6d6c5f9220874022'
SHA='020bacefeffd12bdfa460c165b43b471f9f987b5da7579bdfd219137ceb2c366'

class PreflightTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name);self.old=self.root/'old';self.old.mkdir()
        self.new=self.root/'new';self.data=b'disposable archive fixture'
        (self.old/'images.tar.gz').write_bytes(self.data)
        (self.old/'cleanup.complete').write_text('complete')
        script=Path(__file__).resolve().parents[2]/'docs/qualification/Resume-CohostSetup.ps1'
        line=next(line.strip() for line in script.read_text().splitlines() if 'Remote $address "if [ ! -d' in line)
        self.command=line[len('Remote $address "'):-1].replace('$remote',self.new.as_posix()).replace(OLD,self.old.as_posix()).replace(SHA,hashlib.sha256(self.data).hexdigest())
    def run_command(self):
        bash=r'C:\Program Files\Git\bin\bash.exe' if os.name=='nt' else 'bash'
        # Windows ACLs cannot model POSIX mkdir -m. Linux CI executes it unchanged.
        command=self.command.replace('mkdir -m 700','mkdir') if os.name=='nt' else self.command
        return subprocess.run([bash,'-s'],input=command,text=True,capture_output=True,timeout=15)
    def test_fresh_directory_passes_before_request_is_uploaded(self):
        result=self.run_command();self.assertEqual(result.returncode,0,result.stderr)
        self.assertFalse((self.new/'request.json').exists())
        self.assertEqual((self.new/'images.tar.gz').read_bytes(),self.data)
        self.assertEqual((self.old/'images.tar.gz').stat().st_ino,(self.new/'images.tar.gz').stat().st_ino)
    def test_partial_directory_without_request_can_continue(self):
        self.new.mkdir();os.link(self.old/'images.tar.gz',self.new/'images.tar.gz')
        result=self.run_command();self.assertEqual(result.returncode,0,result.stderr)
    def test_existing_manifest_blocks_restage(self):
        self.new.mkdir();(self.new/'manifest.json').write_text('{}')
        result=self.run_command();self.assertNotEqual(result.returncode,0)
        self.assertIn('refusing restage',result.stderr)
    def test_missing_cleanup_refuses_new_directory(self):
        (self.old/'cleanup.complete').unlink()
        result=self.run_command();self.assertNotEqual(result.returncode,0)
        self.assertFalse(self.new.exists());self.assertIn('cleanup marker missing',result.stderr)
    def test_archive_corruption_fails_checksum(self):
        (self.old/'images.tar.gz').write_bytes(b'corrupt')
        self.assertNotEqual(self.run_command().returncode,0)
    def test_partial_directory_missing_archive_is_explained(self):
        self.new.mkdir();result=self.run_command()
        self.assertNotEqual(result.returncode,0);self.assertIn('archive missing',result.stderr)

if __name__=='__main__':unittest.main()
