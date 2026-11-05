"""Diagnostic classifications only; never changes any qualification gate."""
import json

def summarize(directory,exit_code,failures):
    summary=json.loads((directory/'summary.json').read_text())
    records=[]
    for line in (directory/'reconciliation.jsonl').read_text().splitlines():
        try:records.append(json.loads(line))
        except json.JSONDecodeError:continue
    journal=next((r for r in records if 'offered_write_keys' in r),None)
    integrity=None
    if journal is not None:
        integrity=(journal.get('all_acknowledged_keys_stored_once') is True and journal.get('journal_consistent') is True
            and journal.get('offered_write_keys')==journal.get('exact_acknowledged_keys')==journal.get('stored_write_keys')
            and all(journal.get(k)==0 for k in ('missing_acknowledged_keys','duplicate_stored_keys','unknown_stored_keys','unconfirmed_committed_keys','offered_keys_not_stored')))
    failed=[name+': '+rule for name,metric in summary.get('metrics',{}).items() for rule,value in metric.get('thresholds',{}).items() if value.get('ok') is False]
    return dict(k6_exit_code=exit_code,failed_thresholds=failed,offered_write_inventory_reconciled=integrity,
        original_completion_marker=journal.get('load_process_completed') if journal else None,
        qualification_passed=not failures and exit_code==0,failures=list(failures),scope='diagnostic classification; original gates unchanged')
