import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import baseline
from watchdog import BLOCKING_URLS, ADVISORY_URLS, update_health_counters
from safety import reasons


class BackendHealthScopeTests(unittest.TestCase):
    def run_baseline(self, api_ok=True):
        clock = [0.0]
        def sleep(seconds):
            clock[0] += seconds
        def probe(url, details):
            ok = api_ok if url in BLOCKING_URLS else False
            elapsed = .1 if ok else 2.003
            details[url] = dict(healthy=ok, observer_seconds=elapsed,
                               exit_code=0 if ok else 28, http_status='200' if ok else '000',
                               error='' if ok else 'Operation timed out', http_seconds=elapsed)
            return url, ok, elapsed
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'baseline.json'
            with patch('sys.argv', ['baseline', str(output)]), \
                 patch('baseline.time.monotonic', side_effect=lambda: clock[0]), \
                 patch('baseline.time.time', side_effect=lambda: 1000 + clock[0]), \
                 patch('baseline.time.sleep', side_effect=sleep), \
                 patch('baseline.probe', side_effect=probe), contextlib.redirect_stdout(io.StringIO()):
                if not api_ok:
                    with self.assertRaisesRegex(ValueError, 'baseline health failure'):
                        baseline.main()
                    self.assertFalse(output.exists())
                    self.assertEqual(json.loads(output.with_name('baseline-failure.json').read_text())['url'], BLOCKING_URLS[0])
                    return
                baseline.main()
            doc = json.loads(output.read_text())
            self.assertEqual(set(doc['urls']), set(BLOCKING_URLS))
            self.assertEqual(doc['urls'][BLOCKING_URLS[0]]['successful_samples'], 60)
            rows = [json.loads(row) for row in output.with_name('baseline-observations.jsonl').read_text().splitlines()]
            self.assertEqual(len(rows), 60)
            self.assertTrue(all(not row['probes'][ADVISORY_URLS[0]]['healthy'] for row in rows))
            baseline.validate(doc, 1300)
            doc.pop('health_scope')
            with self.assertRaisesRegex(ValueError, 'health scope'):
                baseline.validate(doc, 1300)

    def test_homepage_timeout_every_sample_is_retained_and_does_not_abort(self):
        self.run_baseline()

    def test_api_timeout_still_aborts_before_load(self):
        self.run_baseline(api_ok=False)

    def test_homepage_cannot_increment_guard_failure_counts_but_api_still_does(self):
        api = BLOCKING_URLS[0]
        failures = {api: 0}
        slow = {api: 0}
        base = {api: .1}
        sample = dict(available_bytes=4*2**30, disk_free_bytes=40*2**30, oom=False,
                      production_healthy=True, cpu_high_seconds=0, iowait_high_seconds=0,
                      health_failures=0, latency_failures=0, peer_heartbeat_age_seconds=0)
        for _ in range(5):
            update_health_counters([(api, True, .1), (ADVISORY_URLS[0], False, 2.003)], base, failures, slow)
        self.assertEqual((failures[api], slow[api]), (0, 0))
        for _ in range(2):
            update_health_counters([(api, False, 2.003)], base, failures, slow)
        self.assertIn('public health', reasons({**sample, 'health_failures': failures[api]}, 'database'))
        for _ in range(3):
            update_health_counters([(api, True, 2.1)], base, failures, slow)
        self.assertIn('public latency', reasons({**sample, 'latency_failures': slow[api]}, 'database'))
        for field, value, expected in [('available_bytes', 1, 'memory reserve'),
                                      ('disk_free_bytes', 1, 'disk reserve'),
                                      ('oom', True, 'OOM'),
                                      ('production_healthy', False, 'production health'),
                                      ('cpu_high_seconds', 5, 'CPU above 85 percent'),
                                      ('iowait_high_seconds', 5, 'IO wait above 10 percent')]:
            self.assertIn(expected, reasons({**sample, field: value}, 'database'))
