import json,tempfile,unittest
from pathlib import Path
from stage_outcome import summarize

class OutcomeTests(unittest.TestCase):
    def test_latency_failure_does_not_claim_missing_writes_or_pass_stage(self):
        with tempfile.TemporaryDirectory() as tmp:
            p=Path(tmp)
            (p/'summary.json').write_text(json.dumps({'metrics':{'services':{'thresholds':{'p95<500':{'ok':False}}}}}))
            journal=dict(offered_write_keys=100,exact_acknowledged_keys=100,stored_write_keys=100,all_acknowledged_keys_stored_once=True,journal_consistent=True,load_process_completed=False)
            for k in ('missing_acknowledged_keys','duplicate_stored_keys','unknown_stored_keys','unconfirmed_committed_keys','offered_keys_not_stored'):journal[k]=0
            (p/'reconciliation.jsonl').write_text(json.dumps(journal))
            result=summarize(p,99,['original k6 gate exit 99'])
            self.assertTrue(result['offered_write_inventory_reconciled']);self.assertFalse(result['qualification_passed'])
            self.assertEqual(result['failed_thresholds'],['services: p95<500'])
            journal['missing_acknowledged_keys']=1
            (p/'reconciliation.jsonl').write_text(json.dumps(journal))
            self.assertFalse(summarize(p,99,['failure'])['offered_write_inventory_reconciled'])
    def test_missing_journal_is_unknown(self):
        with tempfile.TemporaryDirectory() as tmp:
            p=Path(tmp);(p/'summary.json').write_text('{"metrics":{}}');(p/'reconciliation.jsonl').write_text('')
            self.assertIsNone(summarize(p,1,['missing evidence'])['offered_write_inventory_reconciled'])

    def test_retained_early_abort_separates_accounting_from_resource_pressure(self):
        with tempfile.TemporaryDirectory() as tmp:
            p=Path(tmp);(p/'summary.json').write_text('{"metrics":{}}')
            (p/'reconciliation.jsonl').write_text(json.dumps(dict(
                all_acknowledged_keys_stored_once=True,journal_consistent=True,
                offered_write_keys=11,exact_acknowledged_keys=8,stored_write_keys=11,
                unconfirmed_committed_keys=3,load_process_completed=False)))
            checks=dict(coverage=False,duration=False,offered_completed=False,
                cpu_p95=True,burst_cpu_p95=True,throttle=True,memory=True,no_oom=True)
            (p/'actual-generator-gate.json').write_text(json.dumps(dict(assertions=checks,passed=False)))
            result=summarize(p,99,['original k6 gate exit 99'])
            self.assertTrue(result['acknowledged_writes_stored_once'])
            self.assertEqual(result['unconfirmed_committed_writes'],3)
            self.assertFalse(result['offered_write_inventory_reconciled'])
            self.assertFalse(result['generator']['resource_headroom_gate_failed'])
            self.assertEqual(result['generator']['accounting_failures'],['coverage','duration','offered_completed'])
            self.assertFalse(result['qualification_passed'])
            checks['throttle']=False
            (p/'actual-generator-gate.json').write_text(json.dumps(dict(assertions=checks,passed=False)))
            result=summarize(p,99,['failure'])
            self.assertTrue(result['generator']['resource_headroom_gate_failed'])
            self.assertEqual(result['generator']['resource_failures'],['throttle'])
