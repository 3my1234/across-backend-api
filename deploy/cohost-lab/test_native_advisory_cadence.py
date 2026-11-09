import unittest
from unittest.mock import patch
import watchdog as w

class NativeAdvisoryCadence(unittest.TestCase):
    def test_api_never_omitted_and_homepage_runs_every_thirty_seconds(self):
        last=float('-inf');advisory=[]
        for second in range(65):
            urls=w.monitoring_urls(True,second,last)
            self.assertTrue(set(w.BLOCKING_URLS)<=set(urls))
            if set(w.ADVISORY_URLS)&set(urls):last=second;advisory.append(second)
        self.assertEqual(advisory,[0,30,60])
        self.assertEqual(w.monitoring_urls(False,1,0),w.URLS)
    def test_api_failure_counters_unchanged_without_homepage(self):
        url=w.BLOCKING_URLS[0];failures={url:0};slow={url:0}
        for _ in range(2):w.update_health_counters([(url,False,2.01)],{url:.1},failures,slow)
        self.assertEqual(failures[url],2);self.assertEqual(slow[url],2)

if __name__=='__main__':unittest.main()
