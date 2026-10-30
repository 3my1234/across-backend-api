import json, subprocess, tempfile, unittest
from pathlib import Path
from unittest.mock import Mock, patch
from trial_runner import Runner


class StartupTests(unittest.TestCase):
    def runner(self):
        return Runner(Path('.'), Path('ssh_config'), {'run': 'cohost-'+'a'*16}, Path('.'))

    def test_cold_connections_precede_runtime_monitor_and_activation(self):
        runner=self.runner(); events=[]
        def ssh(role, command, timeout=30):
            events.append((role,command,timeout))
            return json.dumps(dict(run=runner.approval['run'],role=role,age_seconds=1,sequence=10)).encode()
        runner.ssh=Mock(side_effect=ssh)
        def started():
            self.assertEqual(sorted(events), [('database','status',15),('main','status',15)])
            runner.previous={'main':11,'database':11}
        runner.leases=started
        runner.start()
        self.assertTrue(runner.activation_done)
        self.assertEqual(sorted(events[2:]), [('database','activate',60),('main','activate',60)])

    def test_failed_or_stale_handshake_never_starts_monitor_or_activation(self):
        for failure in (subprocess.TimeoutExpired('ssh',15),None):
            runner=self.runner()
            def ssh(role,command,timeout=30):
                if failure:raise failure
                return json.dumps(dict(run=runner.approval['run'],role=role,age_seconds=4,sequence=10)).encode()
            runner.ssh=Mock(side_effect=ssh)
            runner.leases=Mock()
            with self.assertRaises((subprocess.TimeoutExpired,ValueError)):runner.start()
            runner.leases.assert_not_called()
            self.assertTrue(all(call.args[1]=='status' for call in runner.ssh.call_args_list))

    def test_recurring_status_deadline_remains_two_seconds(self):
        runner=self.runner()
        runner.ssh=Mock(side_effect=subprocess.TimeoutExpired('ssh',2))
        with tempfile.TemporaryDirectory() as tmp:
            runner.output=Path(tmp)
            with patch('trial_runner.subprocess.run'):runner.leases()
            report=json.loads((runner.output/'peer-relay-failure.json').read_text())
            self.assertEqual(report['phase'],'status')
            self.assertEqual(report['error_class'],'TimeoutExpired')
        self.assertIsInstance(runner.error,subprocess.TimeoutExpired)
        self.assertTrue(all(call.kwargs['timeout']==2 for call in runner.ssh.call_args_list))

    def test_seed_disconnect_does_not_hide_original_relay_failure(self):
        runner=self.runner();runner.error=ValueError('stale peer: observed age4')
        runner.control=Mock(side_effect=subprocess.CalledProcessError(1,'ssh'))
        with self.assertRaisesRegex(RuntimeError,'observed age4'):runner.lab('seed','seed')
