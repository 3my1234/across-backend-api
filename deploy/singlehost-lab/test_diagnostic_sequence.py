import unittest
from unittest.mock import Mock,patch
from runner import execute_diagnostic,SingleHostRunner
from trial_policy import STAGES

class DiagnosticTests(unittest.TestCase):
    def test_only_original_first_stage_and_cleanup(self):
        runner=Mock()
        with patch('runner.signal.alarm',create=True):execute_diagnostic(runner)
        runner.measure.assert_called_once_with(STAGES[0])
        runner.cleanup.assert_called_once()
        runner.notifications.assert_not_called()
    def test_abort_still_cleans_and_never_advances(self):
        runner=Mock();runner.measure.side_effect=RuntimeError('latency')
        with patch('runner.signal.alarm',create=True),self.assertRaisesRegex(RuntimeError,'latency'):execute_diagnostic(runner)
        runner.cleanup.assert_called_once()
        runner.measure.assert_called_once_with(STAGES[0])
    def test_diagnostic_cannot_call_larger_stage(self):
        runner=object.__new__(SingleHostRunner);runner.approval={'execution_mode':'diagnostic20'}
        with self.assertRaisesRegex(ValueError,'cannot advance'):runner.measure(STAGES[1])

