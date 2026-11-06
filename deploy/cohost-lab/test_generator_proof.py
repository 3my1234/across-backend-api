import shutil,tempfile,unittest
from pathlib import Path
from check_generator import check

class GeneratorProofTests(unittest.TestCase):
    def proof(self):return Path(__file__).parent/'proofs/generator-40866958782'
    def test_retained_proof_passes_without_network_or_subprocess(self):
        from unittest.mock import patch
        with patch('subprocess.check_output',side_effect=AssertionError('network forbidden')):
            gate=check(self.proof())
        self.assertEqual(gate['requested_seconds'],1800)
        self.assertTrue(all(gate['assertions'].values()))
    def test_corrected_proof_is_bound_to_its_own_client(self):
        corrected=Path(__file__).parent/'proofs/generator-42178904513'
        gate=check(corrected)
        self.assertEqual(gate['completed'],449841)
        self.assertTrue(gate['independent_generator_gate_passed'])
        with tempfile.TemporaryDirectory() as tmp:
            dest=Path(tmp)/'proof';shutil.copytree(corrected,dest)
            shutil.copyfile(self.proof()/'cpu-2-prewarm/executed-sessions.js',dest/'cpu-2-prewarm/executed-sessions.js')
            with self.assertRaises(ValueError):check(dest)
    def test_gate_and_executed_client_tampering_rejected(self):
        for name in ('gate.json','executed-sessions.js'):
            with self.subTest(name=name),tempfile.TemporaryDirectory() as tmp:
                dest=Path(tmp)/'proof';shutil.copytree(self.proof(),dest)
                file=dest/'cpu-2-prewarm'/name
                file.write_text(file.read_text()+' ')
                with self.assertRaises(ValueError):check(dest)
    def test_missing_or_ambiguous_proof_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp)
            with self.assertRaises(ValueError):check(root)
            shutil.copytree(self.proof(),root/'one');shutil.copytree(self.proof(),root/'two')
            with self.assertRaises(ValueError):check(root)
