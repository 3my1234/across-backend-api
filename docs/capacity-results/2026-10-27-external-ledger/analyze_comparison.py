"""Reproduce comparisons from retained, synthetic-only CI artifacts."""
from pathlib import Path
from datetime import datetime
from collections import defaultdict
import json
import re
import statistics

BASE = Path(__file__).parent / '40587689346' / 'comparison-evidence'

def stamp(value):
    return datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()

def values(summary, metric):
    return summary['metrics'].get(metric, {}).get('values', {})

results = []
for stage in ('1-off', '2-on', '3-on', '4-off'):
    root = BASE / stage
    summary = json.loads((root / 'summary.json').read_text())
    assert all(n == 0 for n in json.loads((root / 'exits.json').read_text()).values())
    for metric in summary['metrics'].values():
        assert all(t['ok'] is True for t in metric.get('thresholds', {}).values())
    assert summary['configuration']['seconds'] == 180
    assert summary['configuration']['target'] == 100
    first, last, completed = float('inf'), 0, 0
    initial = defaultdict(list)
    latency_points = []
    for line in (root / 'timeseries.json').open(encoding='utf-8'):
        row = json.loads(line)
        if row['type'] != 'Point':
            continue
        data, metric = row['data'], row['metric']
        if metric == 'offered_requests':
            first = min(first, stamp(data['time']))
        if metric == 'successful_latency':
            time = stamp(data['time'])
            last = max(last, time)
            completed += 1
            latency_points.append((time, data['tags'].get('op'), data['value']))
    for time, op, duration in latency_points:
        if time <= first + 2:
            initial[op].append(duration)
    cpu = defaultdict(list)
    locks = []
    redis_peaks = []
    sample_time = None
    for line in (root / 'resources.log').read_text().splitlines():
        try:
            sample_time = stamp(line)
            continue
        except ValueError:
            pass
        if sample_time is None or not first <= sample_time <= last:
            continue
        if line.startswith('used_memory:'):
            redis_peaks.append(int(line.split(':', 1)[1]))
        try:
            row = json.loads(line)
        except ValueError:
            continue
        if 'CPUPerc' in row:
            cpu[row['Name']].append(float(row['CPUPerc'].rstrip('%')))
        if 'lock_waiters' in row:
            locks.append(row['lock_waiters'])
    pools = [json.loads(line) for line in (root / 'pools.jsonl').read_text().splitlines() if line.strip()]
    backlog_peak, backlog_age = {}, {}
    for point in pools:
        grouped = defaultdict(int)
        for queue in point['queues']:
            if queue['status'] in ('pending', 'retry', 'sending'):
                grouped[queue['queue']] += queue['count']
                backlog_age[queue['queue']] = max(backlog_age.get(queue['queue'], 0), queue['oldest_age_seconds'])
        for name, count in grouped.items():
            backlog_peak[name] = max(backlog_peak.get(name, 0), count)
    before = [row for row in pools if stamp(row['time']) <= first]
    after = [row for row in pools if stamp(row['time']) >= last]
    start = before[-1] if before else pools[0]
    end = after[0] if after else pools[-1]
    pool_delta = {}
    for api in ('api1', 'api2'):
        pool_delta[api] = {key: end[api][key] - start[api][key] for key in ('acquire_count', 'empty_acquires', 'empty_acquire_wait_ms_total', 'cancelled_acquires')}
    recon = [json.loads(line) for line in (root / 'reconciliation.jsonl').read_text().splitlines() if line.strip()]
    writes = next(row for row in recon if 'write_reconciliation_passed' in row)
    journal = next(row for row in recon if 'offered_write_keys' in row)
    assert writes['write_reconciliation_passed'] and journal['all_acknowledged_keys_stored_once']
    sql = json.loads((root / 'sql-statements.json').read_text())
    role_totals = defaultdict(float)
    for query in sql:
        role_totals[query['rolname']] += query['total_exec_time']
    plans = [float(value) for value in re.findall(r'"Execution Time": ([0-9.]+)', (root / 'inbox-plans.txt').read_text())]
    inbox_sql = [{key: q[key] for key in ('calls', 'mean_exec_time', 'max_exec_time', 'total_exec_time', 'shared_blks_read', 'shared_blks_hit')} for q in sql if q['query'].startswith('WITH inbox AS')]
    result = {
        'stage': stage,
        'planned_active_seconds': 180,
        'first_offered_to_last_completed_seconds': last - first,
        'offered_requests': values(summary, 'offered_requests')['count'],
        'completed_workload_requests': completed,
        'completed_rps_request_window': completed / (last - first),
        'k6_reported_offered_rate_including_setup': values(summary, 'offered_requests')['rate'],
        'established_sessions': values(summary, 'authenticated_sessions')['count'],
        'http_error_rate': values(summary, 'http_req_failed')['rate'],
        'dropped_iterations': values(summary, 'dropped_iterations').get('count', 0),
        'overall_latency_ms': values(summary, 'successful_latency'),
        'operations': {key: metric['values'] for key, metric in summary['metrics'].items() if key.startswith('successful_latency{op:')},
        'cpu_percent_one_core_measured_samples': {key: {'samples': len(points), 'mean': statistics.mean(points), 'peak': max(points)} for key, points in cpu.items()},
        'maximum_sampled_lock_waiters': max(locks, default=None),
        'redis_peak_sampled_bytes': max(redis_peaks, default=None),
        'bracketed_pool_deltas': pool_delta,
        'sampled_send_backlog_peak_including_setup_recovery': backlog_peak,
        'sampled_send_backlog_oldest_age_seconds_including_setup_recovery': backlog_age,
        'write_reconciliation': writes,
        'write_journal': journal,
        'notifications': json.loads((root / 'notification-final.json').read_text()),
        'top80_sql_execution_ms_by_role_including_setup_recovery': dict(role_totals),
        'inbox_sql': inbox_sql,
        'postload_custom_then_generic_repeat_plan_execution_ms': plans,
        'first2s_inbox_samples': len(initial.get('inbox', [])),
        'first2s_inbox_max_ms': max(initial.get('inbox', []), default=None),
        'notes': ['CPU/lock samples are roughly5s, not continuous peak detection.', 'Pool counters bracket measurement within the observer sampling interval.', 'SQL statistics include login setup and readiness recovery; top80 statements only.', 'EXPLAIN runs after load; its buffers are warmed, not a true cold-cache experiment.', 'Mock ACK counter records response generation; worker sent state is separate evidence. No real recipient delivery.'],
    }
    assert result['offered_requests'] == completed
    results.append(result)

target = Path(__file__).parent / 'comparison-analysis.json'
target.write_text(json.dumps(results, indent=2), encoding='utf-8')
for result in results:
    cpu = next(value for key, value in result['cpu_percent_one_core_measured_samples'].items() if 'postgres' in key)
    inbox = result['operations']['successful_latency{op:inbox}']
    print(result['stage'], 'RPS', round(result['completed_rps_request_window'], 2), 'inbox p95/p99', round(inbox['p(95)'], 2), round(inbox['p(99)'], 2), 'PG mean/peak', round(cpu['mean'], 2), cpu['peak'], 'writes', result['write_journal']['exact_acknowledged_keys'])
