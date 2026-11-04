import threading,unittest
from unittest.mock import patch
import watchdog

class ParallelChecksTests(unittest.TestCase):
    def test_inspection_and_all_http_checks_start_before_any_can_finish(self):
        rendezvous=threading.Barrier(4)
        def inspect(ids):
            rendezvous.wait(timeout=2)
            return [{'Running':True}]
        def probe(url,details):
            rendezvous.wait(timeout=2)
            details[url]={'healthy':True}
            return url,True,1
        details={}
        with patch.object(watchdog,'state',side_effect=inspect),patch.object(watchdog,'probe',side_effect=probe):
            results,states=watchdog.collect_checks(['recorded-id'],details)
        self.assertEqual(len(results),3)
        self.assertEqual(states,[{'Running':True}])
        self.assertEqual(len(details),3)

    def test_inspection_failure_is_never_replaced_by_successful_http(self):
        with patch.object(watchdog,'state',side_effect=OSError('inspection unavailable')),patch.object(watchdog,'probe',side_effect=lambda url,details:(url,True,1)):
            with self.assertRaises(OSError):watchdog.collect_checks(['recorded-id'],{})
