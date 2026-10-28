from pathlib import Path
import unittest
from prewarm import candidate,recover,METRICS

class LocalPreparationTest(unittest.TestCase):
 def test_exact_reversible_baseline_transform(self):
  source=(Path(__file__).parents[1]/'session-lab/sessions.js').read_text()
  result=candidate(source)
  self.assertEqual(recover(result),source)
  self.assertIn("randomSeed(20261027+exec.vu.idInTest)",result)
  self.assertIn("authenticated.add(1);started.add(1);loggedIn=true;sleep(Math.random()*3)",result)
  self.assertIn("const bound=bindEstablished(original,data.authenticated,cfg.target)",METRICS)
  for forbidden in ['http.', 'req(', 'sleep(', 'randomSeed(', 'authenticated.add(', 'started.add(']:self.assertNotIn(forbidden,METRICS)
 def test_reject_changed_reviewed_anchors(self):
  with self.assertRaises(ValueError):candidate('unexpected source')
