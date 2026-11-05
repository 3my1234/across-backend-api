import unittest
from unittest.mock import patch
from baseline import baseline_probe

URL='https://api.example.invalid/ready'
DNS=dict(http_status='000',exit_code=28,error='curl: (28) Resolving timed out after 2000 milliseconds')

class BaselineDnsTests(unittest.TestCase):
    def run_probes(self,rows):
        def attempt(url,details):
            detail=rows.pop(0);details[url]=detail
            return url,detail.get('healthy',False),detail.get('http_seconds',2)
        return attempt
    def test_dns_timeout_then_healthy_is_recorded_and_recovers(self):
        details={}
        with patch('baseline.probe',side_effect=self.run_probes([DNS,dict(http_status='200',exit_code=0,healthy=True,http_seconds=.2)])) as probe,patch('baseline.time.sleep'):
            self.assertEqual(baseline_probe(URL,details),(URL,True,.2));self.assertEqual(probe.call_count,2)
            self.assertEqual(details[URL]['dns_failures'],1)
    def test_persistent_dns_is_bounded_and_never_healthy(self):
        details={}
        with patch('baseline.probe',side_effect=self.run_probes([DNS,DNS,DNS])) as probe,patch('baseline.time.sleep'):
            self.assertFalse(baseline_probe(URL,details)[1]);self.assertEqual(probe.call_count,3)
    def test_http_errors_unhealthy_readiness_and_slow_http_never_retried(self):
        for row in (dict(http_status='500',exit_code=0),dict(http_status='200',exit_code=0,healthy=False),dict(http_status='200',exit_code=0,healthy=False,http_seconds=2.1),dict(http_status='000',exit_code=28,error='Operation timed out')):
            with self.subTest(row=row),patch('baseline.probe',side_effect=self.run_probes([row])) as probe:
                self.assertFalse(baseline_probe(URL,{})[1]);self.assertEqual(probe.call_count,1)
    def test_dns_retry_does_not_hide_a_following_http_failure(self):
        with patch('baseline.probe',side_effect=self.run_probes([DNS,dict(http_status='503',exit_code=0)])) as probe,patch('baseline.time.sleep'):
            self.assertFalse(baseline_probe(URL,{})[1]);self.assertEqual(probe.call_count,2)
    def test_expired_dns_budget_prevents_another_attempt(self):
        with patch('baseline.probe',side_effect=self.run_probes([DNS])) as probe,patch('baseline.time.sleep'),patch('baseline.time.monotonic',side_effect=[0,12]):
            self.assertFalse(baseline_probe(URL,{})[1]);self.assertEqual(probe.call_count,1)
