"""Read retained isolated calibration artifacts; never declares production capacity."""
import argparse
from collections import Counter, defaultdict
from datetime import datetime
import json
import math
from pathlib import Path


def percentile(values, q):
    if not values:
        return None
    values = sorted(values)
    rank = (len(values) - 1) * q
    low = math.floor(rank)
    high = math.ceil(rank)
    return values[low] + (values[high] - values[low]) * (rank - low)


def stamp(value):
    return datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()


def analyze(arm):
    arm = Path(arm)
    summary = json.loads((arm / 'summary.json').read_text())
    result = json.loads((arm / 'result.json').read_text())
    started, ended = [], []
    statuses, offered = Counter(), Counter()
    durations = defaultdict(list)
    per_status = defaultdict(Counter)
    vus = 0
    with (arm / 'timeseries.json').open() as stream:
        for line in stream:
            row = json.loads(line)
            if row.get('type') != 'Point':
                continue
            metric, data = row['metric'], row['data']
            tags = data.get('tags', {})
            if metric == 'offered_requests':
                started.append(stamp(data['time']))
                offered[tags.get('op', 'unknown')] += data['value']
            if metric == 'vus' and data['value'] == 500:
                vus += 1
            if tags.get('phase') != 'measured':
                continue
            op = tags.get('op', tags.get('name', 'unknown'))
            status = tags.get('status', 'unknown')
            if metric == 'http_reqs':
                ended.append(stamp(data['time']))
                statuses[status] += data['value']
                per_status[op][status] += data['value']
            if metric == 'http_req_duration' and status.isdigit() and 200 <= int(status) < 400:
                durations[op].append(data['value'])
    if not started or not ended:
        raise ValueError('Missing measured request boundaries; do not fabricate zero-load capacity')
    begin, end = min(started), max(ended)
    resources = defaultdict(lambda: {'cores': [], 'quota_fraction': [], 'seconds': 0.0,
                                    'cpu_seconds': 0.0, 'throttled': 0, 'periods': 0, 'burst_fraction': [],
                                    'peak_memory_bytes': 0, 'events': Counter()})
    previous = {}
    sampler = []
    with (arm / 'cgroups.jsonl').open() as stream:
        for line in stream:
            row = json.loads(line)
            now = row['time_unix']
            if begin <= now <= end:
                sampler.append(row.get('sampler_ms', 0))
            for name, item in row['containers'].items():
                old = previous.get(name)
                previous[name] = (now, item)
                if not old or not begin <= now <= end:
                    continue
                elapsed = now - old[0]
                if not 0 < elapsed <= 1:
                    continue
                before = old[1]
                delta = item['cpu']['usage_usec'] - before['cpu']['usage_usec']
                if delta < 0:
                    continue
                r = resources[name]
                cores = delta / (elapsed * 1e6)
                maximum, period = item['cpu_max'].split()
                r['cores'].append(cores)
                if maximum != 'max':
                    r['quota_fraction'].append(cores / (int(maximum) / int(period)))
                    if now <= begin + 6:
                        r['burst_fraction'].append(cores / (int(maximum) / int(period)))
                r['seconds'] += elapsed
                r['cpu_seconds'] += delta / 1e6
                r['throttled'] += max(0, item['cpu'].get('nr_throttled', 0) - before['cpu'].get('nr_throttled', 0))
                r['periods'] += max(0, item['cpu'].get('nr_periods', 0) - before['cpu'].get('nr_periods', 0))
                r['peak_memory_bytes'] = max(r['peak_memory_bytes'], item['memory_current'])
                r['memory_max'] = item.get('memory_max', 'not retained in older artifact')
                for key, value in item['memory_events'].items():
                    r['events'][key] += max(0, value - before['memory_events'].get(key, 0))
    resource_output = {}
    for name, r in resources.items():
        resource_output[name] = {
            'sampled_seconds': r['seconds'], 'mean_cpu_cores': r['cpu_seconds'] / r['seconds'],
            'p95_cpu_cores': percentile(r['cores'], .95),
            'p95_cpu_quota_fraction': percentile(r['quota_fraction'], .95),
            'initial_six_seconds_p95_cpu_quota_fraction': percentile(r['burst_fraction'], .95),
            'throttled_period_fraction': r['throttled'] / max(1, r['periods']),
            'peak_memory_bytes': r['peak_memory_bytes'], 'memory_max': r['memory_max'],
            'memory_event_deltas': dict(r['events'])}
    thresholds = [name for name, metric in summary['metrics'].items()
                  if any(not value.get('ok', False) for value in metric.get('thresholds', {}).values())]
    seconds = summary['configuration']['seconds']
    original_stage_completed = (all(result.get(key) == 0 for key in ('measured_exit', 'reconcile_exit', 'recovery_exit'))
                                and end - begin >= seconds - 5 and not thresholds)
    return {'configuration': summary['configuration'], 'observed_seconds': end - begin,
            'offered': sum(offered.values()), 'completed': sum(statuses.values()),
            'uncompleted_at_observation_end': sum(offered.values()) - sum(statuses.values()),
            'status_counts': dict(statuses), 'vus500_snapshots_all_phases': vus,
            'failed_original_thresholds': thresholds, 'original_stage_completed': original_stage_completed,
            'result': result, 'per_operation': {op: {'offered': offered[op], 'statuses': dict(per_status[op]),
                'successful_samples': len(durations[op]),
                'successful_p50_ms': percentile(durations[op], .5),
                'successful_p95_ms': percentile(durations[op], .95),
                'successful_p99_ms': percentile(durations[op], .99)} for op in sorted(set(offered) | set(per_status))},
            'resources': resource_output, 'sampler_p99_ms': percentile(sampler, .99),
            'scope': 'Offline artifact summary only. Existing verifier remains authoritative for writes. '
                     'No production or sustained500 capacity claim; resource and shared-host headroom require review.'}


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('arm')
    args = parser.parse_args()
    print(json.dumps(analyze(args.arm), indent=2))
