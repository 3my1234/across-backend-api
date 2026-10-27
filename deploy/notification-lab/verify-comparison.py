"""Fail closed: fault experiments require all four exact 100-session gates."""
import json
from pathlib import Path

proof = json.loads(Path('comparison-proof.json').read_text())
assert proof['conclusion'] == 'success'
assert proof['headBranch'] == 'experiment/notification-workers'
assert len(proof['headSha']) == 40
for stage in ('1-off', '2-on', '3-on', '4-off'):
    root = Path('approved-comparison/comparison-evidence') / stage
    exits = json.loads((root / 'exits.json').read_text())
    assert set(exits) == {'measured_exit', 'reconcile_exit', 'notification_verify_exit', 'gate_exit'}
    assert all(value == 0 for value in exits.values())
    assert json.loads((root / 'gate.json').read_text())['previous_sessions'] == 100
    summary = json.loads((root / 'summary.json').read_text())
    assert summary['configuration']['seconds'] == 180
    assert summary['configuration']['target'] == 100
    assert summary['configuration']['p95_ms'] == 500
    assert summary['configuration']['p99_ms'] == 1500
    for metric in summary['metrics'].values():
        assert all(t['ok'] is True for t in metric.get('thresholds', {}).values())
print('Four unchanged 100-session comparison gates independently checked. No 500-session approval.')
