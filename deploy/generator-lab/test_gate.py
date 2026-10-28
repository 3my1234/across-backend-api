import unittest
from gate import percentile

class GateTest(unittest.TestCase):
    def test_percentile_and_missing_telemetry(self):
        self.assertIsNone(percentile([], .95))
        self.assertEqual(percentile([1,4,2,3],1),4)
        self.assertEqual(percentile([1,4,2,3],0),1)
