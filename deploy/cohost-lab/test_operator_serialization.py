"""Execute the actual operator credential-building snippet with dummy files only."""
import os,shutil,subprocess,tempfile,unittest
from pathlib import Path

SHELL=shutil.which('powershell.exe' if os.name=='nt' else 'pwsh')

@unittest.skipUnless(SHELL,'PowerShell unavailable')
class OperatorSerializationTests(unittest.TestCase):
    def test_generated_bundle_remains_small_and_keys_roundtrip_as_strings(self):
        source=Path(__file__).with_name('prepare_trial_execution.py').read_text()
        build='    $bundleObject = @{'+source.split('    $bundleObject = @{',1)[1].split('    $secretInstalled',1)[0]
        validate='        $bundleJson ='+source.split('        $bundleJson =',1)[1].split("        Write-Host 'Uploading",1)[0]
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp);(base/'main').mkdir()
            (base/'ci-main').write_text('dummy-main-key')
            (base/'ci-database').write_text('dummy-db-key')
            (base/'main/database_known_hosts').write_text('dummy-pinned-host')
            (base/'main/trial-approval.json').write_text('{"run":"synthetic","load_authorized":true}')
            quoted=str(base).replace("'","''")
            script="$ErrorActionPreference='Stop';$private='"+quoted+"';$review=$private;$mainPin='dummy';\n"+build+validate+"\nWrite-Output 'ROUNDTRIP_OK'"
            result=subprocess.run([SHELL,'-NoProfile','-NonInteractive','-Command',script],text=True,capture_output=True,timeout=10)
            self.assertEqual(result.returncode,0,result.stderr)
            self.assertIn('ROUNDTRIP_OK',result.stdout)
