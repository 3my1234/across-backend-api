"""Reconcile the retained real-delay mock-worker experiment independently."""
from pathlib import Path
from collections import defaultdict
from datetime import datetime
import json
import statistics

ROOT = Path(__file__).parent
DATA = ROOT / '40591221683' / 'session-evidence'
def stamp(value):
    return datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()
summary = json.loads((DATA / 'summary.json').read_text())
assert summary['configuration']['seconds'] == 1800 and summary['configuration']['target'] == 100
for metric in summary['metrics'].values():
    assert all(t['ok'] is True for t in metric.get('thresholds', {}).values())
metrics = summary['metrics']
values = lambda name: metrics.get(name, {}).get('values', {})
first, last, completed = float('inf'), 0, 0
vus = []
for line in (DATA / 'timeseries.json').open(encoding='utf-8'):
    row = json.loads(line)
    if row['type'] != 'Point':
        continue
    if row['metric'] == 'offered_requests':
        first = min(first, stamp(row['data']['time']))
    if row['metric'] == 'successful_latency':
        last = max(last, stamp(row['data']['time']))
        completed += 1
    if row['metric'] == 'vus':
        vus.append((stamp(row['data']['time']), row['data']['value']))
assert completed == values('offered_requests')['count']
rows = [json.loads(line) for line in (DATA / 'reconciliation.jsonl').read_text().splitlines() if line.strip()]
journal = next(r for r in rows if 'exact_acknowledged_keys' in r)
writes = next(r for r in rows if 'write_reconciliation_passed' in r)
assert journal['normal_completion_inventory_matched'] and writes['write_reconciliation_passed']
cpu, locks, redis = defaultdict(list), [], []
time = None
for line in (DATA / 'resources.log').read_text().splitlines():
    try:
        time = stamp(line)
        continue
    except ValueError:
        pass
    if time is None or not first <= time <= last:
        continue
    if line.startswith('used_memory:'):
        redis.append(int(line.split(':')[1]))
    try:
        row = json.loads(line)
    except ValueError:
        continue
    if 'CPUPerc' in row:
        cpu[row['Name']].append(float(row['CPUPerc'].rstrip('%')))
    if 'lock_waiters' in row:
        locks.append(row['lock_waiters'])
pools = [json.loads(line) for line in (DATA / 'pools.jsonl').read_text().splitlines() if line.strip()]
start = [r for r in pools if stamp(r['time']) <= first][-1]
end = [r for r in pools if stamp(r['time']) >= last][0]
delta = {api: {k: end[api][k] - start[api][k] for k in ('acquire_count', 'empty_acquires', 'empty_acquire_wait_ms_total', 'cancelled_acquires')} for api in ('api1', 'api2')}
peak, ages = {}, {}
for row in pools:
    counts = defaultdict(int)
    for q in row['queues']:
        if q['status'] in ('pending', 'retry', 'sending'):
            counts[q['queue']] += q['count']
            ages[q['queue']] = max(ages.get(q['queue'], 0), q['oldest_age_seconds'])
    for name, count in counts.items():
        peak[name] = max(peak.get(name, 0), count)
final = json.loads((DATA / 'notification-final.json').read_text())
assert final['killed_jobs'] == 89 and final['killed_jobs_recovered'] == 89
assert not any(final['sent_without_mock_ack'].values())
assert set(final['email_status']) == {'sent'} and set(final['push_status']) <= {'sent', 'delivered'}
assert (DATA / 'send-backlog-drained.txt').read_text().strip() == 'true'
fault_text = (DATA / 'faults.log').read_text()
fault_snapshots = []
for line in fault_text.splitlines():
    if line.startswith('{'):
        fault_snapshots.append(json.loads(line))
assert [r['killed_jobs_recovered'] for r in fault_snapshots] == [0, 89]
# Journal ACK is generated at the provider, not a wire-receipt guarantee.
# Approximate ages below use its last generated ACK, not database sent_at.
ack, receipt = {}, {}
for line in (DATA / 'provider-journal.jsonl').open(encoding='utf-8'):
    event = json.loads(line)
    if event['channel'] != 'push':
        continue
    if event['event'] == 'ack':
        ack[event['key']] = stamp(event['time'])
    if event['event'] == 'receipt':
        receipt.setdefault(event['key'], stamp(event['time']))
final_time = stamp(final['time'])
outstanding = [final_time - t for key, t in ack.items() if key not in receipt and final_time - t >= 900]
latency = [receipt[key] - ack[key] for key in receipt]
report = {
    'source_commit': (DATA / 'source-commit.txt').read_text().strip(),
    'sessions': values('authenticated_sessions')['count'],
    'active_seconds': 1800,
    'observed_vus_100_samples': sum(n == 100 for _, n in vus),
    'sampled_dwell_100_seconds': sum(b[0] - a[0] for a, b in zip(vus, vus[1:]) if a[1] == 100),
    'request_window_seconds': last - first,
    'offered_completed_requests': completed,
    'request_window_rps': completed / (last - first),
    'overall_latency_ms': values('successful_latency'),
    'operations': {k: m['values'] for k, m in metrics.items() if k.startswith('successful_latency{op:')},
    'http_errors_rate': values('http_req_failed')['rate'],
    'dropped_iterations': values('dropped_iterations').get('count', 0),
    'poll_latency_ms': values('poll_delivery_latency'),
    'write_journal': journal,
    'writes': writes,
    'recipient_visibility': next(r for r in rows if 'post_run_recipient_visibility_passed' in r),
    'cpu_percent_one_core_measured_5s_samples': {k: {'samples': len(v), 'mean': statistics.mean(v), 'peak': max(v)} for k, v in cpu.items()},
    'pool_delta': delta,
    'sampled_lock_waiters_max': max(locks),
    'sampled_redis_used_memory_max': max(redis),
    'sampled_send_backlog_peak': peak,
    'sampled_send_backlog_age_max_seconds': ages,
    'notifications': final,
    'lease_snapshots': fault_snapshots,
    'approx_provider_ack_based_due_receipt_keys': len(outstanding),
    'approx_provider_ack_based_oldest_due_age_seconds': max(outstanding),
    'provider_ack_to_mock_receipt_mean_seconds': statistics.mean(latency),
    'provider_ack_to_mock_receipt_max_seconds': max(latency),
    'limitation': 'Five-second CPU/queue sampling; provider ACK generation is distinct from worker completion and real recipient delivery. Receipt ages above are provider-journal approximations, not database sent_at ages.',
}
(ROOT / 'fault-analysis.json').write_text(json.dumps(report, indent=2), encoding='utf-8')
print(json.dumps({k: report[k] for k in ('source_commit', 'offered_completed_requests', 'request_window_rps', 'overall_latency_ms', 'pool_delta', 'sampled_send_backlog_peak', 'sampled_send_backlog_age_max_seconds', 'approx_provider_ack_based_oldest_due_age_seconds')}, indent=2))
for name, row in report['cpu_percent_one_core_measured_5s_samples'].items():
    print(name, round(row['mean'], 2), row['peak'])
