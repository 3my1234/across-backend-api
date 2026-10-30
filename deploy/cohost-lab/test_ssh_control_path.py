import json,os,socket,subprocess,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
from install_ci_bundle import control_path,install,cleanup

class ControlPathTests(unittest.TestCase):
    def test_long_workspace_path_is_rejected_but_short_path_has_suffix_reserve(self):
        with self.assertRaises(ValueError):control_path(Path('/home/runner/work/across-backend-api/across-backend-api/.cohost-private'))
        value=control_path(Path('/tmp/axct-12345678')).replace('%C','f'*40)
        self.assertLessEqual(len((value+'.'+'a'*16).encode()),103)
    @unittest.skipUnless(os.name=='posix','Linux Unix-domain socket regression')
    def test_actual_generated_config_uses_secure_short_socket_under_long_workspace(self):
        with tempfile.TemporaryDirectory() as tmp:
            output=Path(tmp)/('workspace-'+'x'*150)/'.cohost-private';output.parent.mkdir()
            from test_trial import TrialPolicyTests
            a=TrialPolicyTests().approval()
            bundle=dict(approval=a,main_key='-----BEGIN OPENSSH PRIVATE KEY-----\ndummy\n',database_key='-----BEGIN OPENSSH PRIVATE KEY-----\ndummy\n',main_known_hosts='dummy-pin',database_known_hosts='dummy-pin')
            install(bundle,output)
            try:
                data=json.loads((output/'socket-directory.json').read_text());directory=Path(data['path'])
                self.assertEqual(directory.parent,Path('/tmp'));self.assertEqual(directory.stat().st_mode&0o777,0o700)
                for role in ('main','database'):
                    result=subprocess.check_output(['ssh','-G','-F',str(output/'ssh_config'),'cohost-'+role],text=True,stderr=subprocess.DEVNULL)
                    value=next(line.split(' ',1)[1] for line in result.splitlines() if line.startswith('controlpath '))
                    self.assertNotIn('%C',value);self.assertLessEqual(len(value.encode())+17,103)
                    # Exercise the real kernel's socket-length check including worst-case SSH suffix.
                    target=value+'.'+'a'*16
                    with socket.socket(socket.AF_UNIX) as endpoint:endpoint.bind(target)
                    Path(target).unlink()
            finally:
                with patch('install_ci_bundle.subprocess.run'):cleanup(output)
            self.assertFalse(directory.exists())
