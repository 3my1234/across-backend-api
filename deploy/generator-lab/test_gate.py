import unittest
from gate import percentile,reviewed_profile

class GateTest(unittest.TestCase):
    def test_percentile_and_missing_telemetry(self):
        self.assertIsNone(percentile([], .95))
        self.assertEqual(percentile([1,4,2,3],1),4)
        self.assertEqual(percentile([1,4,2,3],0),1)

class DurationPolicyTest(unittest.TestCase):
    def test_only_reviewed_duration_and_population_are_accepted(self):
        self.assertEqual(reviewed_profile({'profile':'stage','seconds':180,'target':500}),('stage',180))
        self.assertEqual(reviewed_profile({'profile':'sustained','seconds':1800,'target':500}),('sustained',1800))
        for item in [
            {'profile':'sustained','seconds':180,'target':500},
            {'profile':'stage','seconds':1800,'target':500},
            {'profile':'sustained','seconds':1800,'target':1000},
            {'profile':'other','seconds':1800,'target':500},
        ]:
            with self.assertRaises(ValueError):reviewed_profile(item)
