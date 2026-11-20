import unittest
import collect
import io
from unittest.mock import patch
from urllib.error import HTTPError


class CapacityTests(unittest.TestCase):
    def test_probe_identity_and_edge_denial(self):
        response = io.BytesIO(b'{"ok":true}')
        response.status = 200
        with patch.object(collect.urllib.request, 'urlopen', return_value=response) as request:
            self.assertTrue(collect.probe_ready()['api_ready'])
            self.assertEqual(request.call_args.args[0].get_header('User-agent'), 'ATL-Capacity-Monitor/1.0')
        with patch.object(collect.urllib.request, 'urlopen', side_effect=HTTPError('https://api.atlxpres.com',403,'Forbidden',{},None)):
            checks = collect.probe_ready()
        self.assertTrue(checks['probe_rejected'])
        self.assertFalse(checks['api_ready'])
        report = {'host':{},'database':{},'checks':checks,'containers':[]}
        streaks={}
        for _ in range(3): alerts,streaks=collect.assess(report,streaks)
        self.assertIn('api_probe_rejected',alerts)
        self.assertNotIn('api_unavailable',alerts)

    def test_percentile_is_bucket_bound_not_average(self):
        self.assertIsNone(collect.percentile([0]*8))
        self.assertEqual(collect.percentile([0, 0, 96, 0, 0, 0, 0, 4]), 100)
        self.assertEqual(collect.percentile([0, 0, 90, 0, 0, 0, 0, 10]), '>3000')

    def test_restart_baseline_and_counter_deltas(self):
        old = {'http': {'uptime_seconds': 100, 'groups': {'chat': {'requests': 20, 'server_errors': 0, 'duration_bucket_counts': [0, 0, 20, 0, 0, 0, 0, 0]}}},
               'pool': {'acquires': 20, 'wait_ms_total': 4000, 'empty_acquires': 2, 'cancelled': 0}}
        new = {'http': {'uptime_seconds': 160, 'groups': {'chat': {'requests': 80, 'server_errors': 1, 'duration_bucket_counts': [0, 0, 80, 0, 0, 0, 0, 0]}}},
               'pool': {'acquires': 80, 'wait_ms_total': 4060, 'empty_acquires': 3, 'cancelled': 1}}
        delta = collect.runtime_delta(new, old, 60)
        self.assertEqual(delta['groups']['chat']['rps'], 1)
        self.assertEqual(delta['groups']['chat']['p95_upper_ms'], 100)
        self.assertEqual(delta['pool_wait_ms_per_acquire'], 1)
        self.assertEqual(delta['pool_cancelled'], 1)
        self.assertTrue(collect.runtime_delta(new, None, 60)['baseline'])
        new['http']['uptime_seconds'] = 1
        self.assertTrue(collect.runtime_delta(new, old, 60)['baseline'])

    def test_alerts_require_sustained_conditions_and_missing_is_not_healthy(self):
        report = {'host': {'cpu_percent': 90, 'memory_used_percent': 20, 'disk_used_percent': 10},
                  'database': {'connections': 10, 'max_connections': 100},
                  'checks': {'api_ready': True}, 'containers': []}
        alerts, streaks = collect.assess(report, {})
        self.assertEqual(alerts, [])
        alerts, streaks = collect.assess(report, streaks)
        self.assertEqual(alerts, [])
        alerts, streaks = collect.assess(report, streaks)
        self.assertIn('host_cpu', alerts)
        report['host']['cpu_percent'] = 5
        self.assertNotIn('host_cpu', collect.assess(report, streaks)[0])
        del report['database']
        for _ in range(3):
            alerts, streaks = collect.assess(report, streaks)
        self.assertIn('database_unavailable', alerts)


if __name__ == '__main__':
    unittest.main()
