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
