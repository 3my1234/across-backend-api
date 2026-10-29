import unittest
from gate import memory_headroom

class MemoryBudgetTests(unittest.TestCase):
    def test_headroom_fraction_remains_strict(self):
        for budget in (2*1024**3,4*1024**3):
            self.assertTrue(memory_headroom(.69*budget,budget,budget))
            self.assertFalse(memory_headroom(.7*budget,budget,budget))
            self.assertFalse(memory_headroom(.71*budget,budget,budget))
    def test_declared_budget_cannot_hide_actual_limit(self):
        with self.assertRaises(ValueError):memory_headroom(1,4*1024**3,2*1024**3)
        with self.assertRaises(ValueError):memory_headroom(1,8*1024**3,8*1024**3)
