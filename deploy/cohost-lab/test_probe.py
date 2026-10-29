import subprocess,unittest
from unittest.mock import patch
from watchdog import probe,run_probe_process

class ProbeTests(unittest.TestCase):
    def test_ready_must_be_true_and_tls_deadline_not_weakened(self):
        with patch('watchdog.run_probe_process',return_value=subprocess.CompletedProcess([],0,'{"ok":true}\n200','')) as call:
            self.assertTrue(probe('https://api.atlxpres.com/api/v1/ready')[1])
            args=call.call_args.args[0]
            self.assertEqual(args[:2],['curl','-q'])
            self.assertEqual(args[args.index('--max-time')+1],'2')
            self.assertEqual(args[args.index('--proto')+1],'=https')
            self.assertNotIn('--insecure',args);self.assertNotIn('--retry',args)
    def test_unhealthy_timeout_certificate_failure_and_malformed_fail_closed(self):
        for result in (subprocess.CompletedProcess([],0,'{"ok":false}\n200',''),
                       subprocess.CompletedProcess([],0,'{"ok":true}\n503',''),
                       subprocess.CompletedProcess([],28,'{"ok":true}\n200',''),
                       subprocess.CompletedProcess([],60,'\n000',''),
                       subprocess.CompletedProcess([],0,'invalid\n200','')):
            with self.subTest(result=result),patch('watchdog.run_probe_process',return_value=result):
                self.assertFalse(probe('https://api.atlxpres.com/api/v1/ready')[1])
        with patch('watchdog.run_probe_process',side_effect=subprocess.TimeoutExpired('curl',2.5)):
            self.assertFalse(probe('https://atlxpres.com/')[1])
    def test_website_body_discarded_and_process_overhead_remains_in_elapsed(self):
        with patch('watchdog.run_probe_process',return_value=subprocess.CompletedProcess([],0,'\n200','')) as call,patch('watchdog.time.monotonic',side_effect=[10,12.1]):
            _,healthy,seconds=probe('https://atlxpres.com/')
            self.assertTrue(healthy);self.assertGreater(seconds,2)
            self.assertNotIn('--max-filesize',call.call_args.args[0])
    def test_actual_failed_request_keeps_phases_and_curl_error(self):
        details={}
        result=subprocess.CompletedProcess([],28,'\n000\t0.010\t0.020\t0.000\t0.000\t2.000\t0','curl: (28) SSL connection timeout')
        with patch('watchdog.run_probe_process',return_value=result):
            self.assertFalse(probe('https://atlxpres.com/',details)[1])
        row=details['https://atlxpres.com/']
        self.assertEqual(row['exit_code'],28)
        self.assertEqual(row['phases']['dns_seconds'],'0.010')
        self.assertEqual(row['phases']['tls_seconds'],'0.000')
        self.assertIn('SSL connection timeout',row['error'])
        self.assertNotIn('body',row)
    def test_ready_body_still_validated_with_timing_metadata(self):
        with patch('watchdog.run_probe_process',return_value=subprocess.CompletedProcess([],0,'{"ok":true}\n200\t0.001\t0.002\t0.003\t0.004\t0.005\t238','')):
            self.assertTrue(probe('https://api.atlxpres.com/api/v1/ready')[1])

class ProcessTimingTests(unittest.TestCase):
    def test_launch_and_wait_reported_separately(self):
        from unittest.mock import MagicMock
        process=MagicMock();process.__enter__.return_value=process
        process.communicate.return_value=('\n200','');process.returncode=0
        detail={}
        with patch('watchdog.subprocess.Popen',return_value=process),patch('watchdog.time.monotonic',side_effect=[10,10.8,10.8,12.1]):
            result=run_probe_process(['curl'],detail)
        self.assertEqual(result.returncode,0)
        self.assertAlmostEqual(detail['process_launch_seconds'],.8)
        self.assertAlmostEqual(detail['process_wait_seconds'],1.3)
        process.communicate.assert_called_once_with(timeout=2.5)
    def test_process_timeout_kills_and_reaps_child(self):
        from unittest.mock import MagicMock
        process=MagicMock();process.__enter__.return_value=process
        process.communicate.side_effect=[subprocess.TimeoutExpired('curl',2.5),('','')]
        detail={}
        with patch('watchdog.subprocess.Popen',return_value=process):
            with self.assertRaises(subprocess.TimeoutExpired):run_probe_process(['curl'],detail)
        process.kill.assert_called_once();self.assertTrue(detail['process_timeout'])

if __name__=='__main__':unittest.main()
