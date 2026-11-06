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
    acknowledged=(journal.get('all_acknowledged_keys_stored_once') is True if journal else None)
    generator=None
    generator_path=directory/'actual-generator-gate.json'
    if generator_path.exists():
        report=json.loads(generator_path.read_text())
        checks=report.get('assertions',{})
        resource_names=('cpu_p95','burst_cpu_p95','throttle','memory','no_oom')
        resource_failed=[name for name in resource_names if checks.get(name) is False]
        accounting_failed=[name for name,value in checks.items() if value is False and name not in resource_names]
        generator=dict(resource_failures=resource_failed,accounting_failures=accounting_failed,
            resource_headroom_gate_failed=bool(resource_failed),
            qualification_passed=report.get('passed') is True,
            scope='partial samples cannot establish sustained generator headroom')
    return dict(k6_exit_code=exit_code,failed_thresholds=failed,offered_write_inventory_reconciled=integrity,
        acknowledged_writes_stored_once=acknowledged,
        unconfirmed_committed_writes=journal.get('unconfirmed_committed_keys') if journal else None,
        generator=generator,
        original_completion_marker=journal.get('load_process_completed') if journal else None,
        qualification_passed=not failures and exit_code==0,failures=list(failures),scope='diagnostic classification; original gates unchanged')
