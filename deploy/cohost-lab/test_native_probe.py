import json,unittest
from unittest.mock import Mock,patch
import native_probe as n

class NativeProbe(unittest.TestCase):
    def test_original_deadlines_and_transport_failures_cannot_pass(self):
        self.assertTrue(n.accepted(dict(transport_healthy=True,http_seconds=2),2.5))
        for http,observer,valid in ((2.001,1,True),(1,2.501,True),(0,1,True),(float('nan'),1,True),(1,1,False)):
            self.assertFalse(n.accepted(dict(transport_healthy=valid,http_seconds=http),observer))
    def test_timeout_kills_stuck_worker_and_does_not_report_health(self):
        observer=n.Observer(n.URLS[0]);observer.process=Mock();observer.process.is_alive.return_value=True;observer.pipe=Mock();observer.pipe.poll.return_value=False
        process=observer.process;detail=observer.probe()
        self.assertFalse(detail['healthy']);self.assertTrue(detail['process_timeout']);process.kill.assert_called_once()
    def test_only_fixed_https_health_urls_can_start_a_worker(self):
        for url in ('http://api.atlxpres.com/api/v1/ready','https://arbitrary.invalid/','file:///root/secret'):
            with self.assertRaises(ValueError):n.Observer(url)
    def test_handle_must_keep_tls_verification_timeouts_and_https_only(self):
        lib=Mock();lib.curl_global_init.return_value=0;lib.curl_easy_init.return_value=123;lib.curl_easy_setopt.return_value=0
        with patch.object(n.ctypes.util,'find_library',return_value='existing'),patch.object(n.C,'CDLL',return_value=lib):
            engine=n.Curl(n.URLS[0])
            options={c.args[1]:c.args[2] for c in lib.curl_easy_setopt.call_args_list}
            self.assertEqual(options[64].value,1);self.assertEqual(options[81].value,2)
            self.assertEqual(options[155].value,2000);self.assertEqual(options[156].value,2000)
            self.assertEqual(options[10318].value,b'https');self.assertEqual(options[10319].value,b'https')
            engine.close()
    def test_readiness_body_is_bounded_even_without_content_length(self):
        engine=object.__new__(n.Curl);engine.ready=True;engine.body=bytearray(b'x'*8192);engine.downloaded=8192
        self.assertEqual(engine.write(None,1,1,None),0)

if __name__=='__main__':unittest.main()
