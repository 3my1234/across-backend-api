import base64,contextlib,io,json,tempfile,time,types,unittest
from pathlib import Path
from unittest.mock import Mock,patch
import setup,entry

class SetupTests(unittest.TestCase):
    def test_manager_nonce_rejects_traversal_and_production_names(self):
        for name in ('../across_db','production','cohost-'+'a'*16+'/other'):
            with self.assertRaises(ValueError):setup.base_for(name)
    def test_public_key_mode_and_expiry_checked_before_any_host_operation(self):
        keys={n:'ssh-ed25519 AAAA synthetic' for n in ('ci-main','ci-database','main-database')}
        future=time.strftime('%Y%m%d%H%M%SZ',time.gmtime(time.time()+10800))
        for live,expiry,publics in (('false',future,keys),(False,'20990101000000Z',keys),(False,future,{**keys,'other':'value'}),(False,future,{**keys,'ci-main':'ssh-ed25519 AAAA\ncommand=bad'})):
            with patch.object(setup,'call') as call, self.assertRaises(ValueError):setup.initialize({},'cohost-'+'a'*16,publics,live,expiry)
            call.assert_not_called()
    def test_logged_subprocess_does_not_pass_stdout_to_check_output(self):
        with patch.object(setup.subprocess,'run') as run,patch.object(setup.subprocess,'check_output') as output:
            setup.call(['safe'],stdout=Mock())
        run.assert_called_once();output.assert_not_called()
    def test_manager_transport_is_noninteractive_and_fixed_alias(self):
        config={'manager_config':'/root/atlxpres-native/manager.conf'}
        with patch.object(setup,'call',return_value=b'{}') as call:
            setup.manager(config,dict(action='status',run='cohost-'+'a'*16))
        args=call.call_args.args[0]
        self.assertIn('BatchMode=yes',args);self.assertIn('cohost-database-manager',args)
        op=json.loads(base64.b64decode(args[-1][13:]));self.assertEqual(op['action'],'status')
    def test_no_load_flag_must_be_explicit_and_never_converts_to_live(self):
        with tempfile.TemporaryDirectory() as tmp,patch.object(entry,'ROOT',Path(tmp)),patch.dict('sys.modules',{'fcntl':Mock()}):
            with patch.object(entry.os,'name','posix'),patch.object(entry.os,'geteuid',return_value=0,create=True),patch.object(entry.sys,'argv',['entry.py','start','--prepare-only']),patch.object(entry,'start') as start:
                entry.main();start.assert_called_once_with(False)
            with patch.object(entry.os,'name','posix'),patch.object(entry.os,'geteuid',return_value=0,create=True),patch.object(entry.sys,'argv',['entry.py','start']),patch.object(entry,'start') as start:
                with self.assertRaises(ValueError):entry.main()
                start.assert_not_called()
    def test_manager_cleanup_progress_cannot_corrupt_json_receipt(self):
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp)/('cohost-'+'a'*16);base.mkdir()
            (base/'native-origin.json').write_text(json.dumps(dict(created_by='native-manager')))
            command='native-setup '+base64.b64encode(json.dumps(dict(action='cleanup',run=base.name)).encode()).decode()
            stdout=io.StringIO();stderr=io.StringIO()
            def child(args,**kwargs):
                self.assertEqual(args[-1],'cleanup')
                self.assertIs(kwargs['stdout'],stderr)
                print('Removed exact test key lines.',file=kwargs['stdout'])
            with patch.object(entry,'catalog',return_value=dict(role='database')),patch.object(entry,'base_for',return_value=base),patch.object(entry.subprocess,'run'),patch.object(entry,'call',side_effect=child),patch.dict('os.environ',SSH_ORIGINAL_COMMAND=command),contextlib.redirect_stdout(stdout),contextlib.redirect_stderr(stderr):
                entry.database_access()
            self.assertTrue(json.loads(stdout.getvalue())['cleanup_confirmed'])
            self.assertIn('Removed exact test key lines.',stderr.getvalue())

if __name__=='__main__':unittest.main()
