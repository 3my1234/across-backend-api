#!/usr/bin/env python3
"""Minute snapshots on each VPS. Aggregate data only; no request logs or credentials.

Run by a bounded oneshot systemd service as root to read Docker/cgroup counters.
Database writes are restricted to ops_capacity_samples and seven-day retention.
The local latest.json remains available if the API/database cannot accept a sample.
"""
import base64
import datetime
import json
import math
import os
import pathlib
import shutil
import smtplib
import ssl
import sys
from email.message import EmailMessage
import subprocess
import time
import urllib.request
import urllib.error

ROOT = pathlib.Path('/var/lib/atl-monitor')
BOUNDS = [10, 50, 100, 250, 500, 1000, 3000, None]


def command(args, stdin=None, timeout=12):
    r = subprocess.run(args, input=stdin, capture_output=True, text=True, timeout=timeout)
    if r.returncode:
        raise RuntimeError('monitor_command_failed')  # never print raw tool errors
    return r.stdout.strip()


def atomic(path, value):
    temporary = path.with_suffix('.pending')
    temporary.write_text(json.dumps(value, allow_nan=False, separators=(',', ':')))
    temporary.chmod(0o600)
    os.replace(temporary, path)


def percentile(buckets, fraction=.95):
    count = sum(buckets)
    if not count:
        return None
    threshold = math.ceil(count * fraction)
    cumulative = 0
    for upper, n in zip(BOUNDS, buckets):
        cumulative += n
        if cumulative >= threshold:
            return upper if upper is not None else '>3000'


def runtime_delta(current, previous, seconds):
    # Counters reset on replacement/restart: the first sample is a baseline.
    if not previous or seconds <= 0 or current['http']['uptime_seconds'] < previous['http']['uptime_seconds']:
        return {'baseline': True}
    result = {'baseline': False, 'groups': {}}
    for name, group in current['http'].get('groups', {}).items():
        old = previous['http'].get('groups', {}).get(name)
        if not old:
            continue
        buckets = [max(0, a-b) for a, b in zip(group['duration_bucket_counts'], old['duration_bucket_counts'])]
        count = sum(buckets)
        result['groups'][name] = {'requests': count, 'rps': round(count/seconds, 2),
                                  'p95_upper_ms': percentile(buckets),
                                  'server_errors': max(0, group['server_errors']-old['server_errors'])}
    pool, old = current['pool'], previous['pool']
    acquires = max(0, pool['acquires']-old['acquires'])
    result['pool_wait_ms_per_acquire'] = round(max(0, pool['wait_ms_total']-old['wait_ms_total'])/acquires, 2) if acquires else 0
    result['pool_empty_acquires'] = max(0, pool['empty_acquires']-old['empty_acquires'])
    result['pool_empty_wait_ms_total']=max(0,pool.get('empty_wait_ms_total',0)-old.get('empty_wait_ms_total',0))
    result['pool_cancelled'] = max(0, pool['cancelled']-old['cancelled'])
    return result


def probe_ready():
    started = time.monotonic()
    result = {'api_ready': False}
    try:
        request = urllib.request.Request('https://api.atlxpres.com/api/v1/ready',
                  headers={'User-Agent': 'ATL-Capacity-Monitor/1.0', 'Cache-Control': 'no-cache'})
        with urllib.request.urlopen(request, timeout=8) as response:
            result['api_status_code'] = response.status
            result['api_ready'] = response.status == 200 and json.load(response).get('ok') is True
    except urllib.error.HTTPError as error:
        result['api_status_code'] = error.code
        result['probe_rejected'] = error.code in (401, 403)
    except Exception:
        pass
    result['ready_ms'] = round((time.monotonic()-started)*1000)
    return result


def collect(role, previous):
    now = time.time()
    interval = now-previous.get('time', now)
    memory = {k: int(v.split()[0])*1024 for k, v in
              (line.split(':', 1) for line in pathlib.Path('/proc/meminfo').read_text().splitlines())}
    cpu = [int(n) for n in pathlib.Path('/proc/stat').read_text().splitlines()[0].split()[1:9]]
    old_cpu = previous.get('cpu', cpu)
    total = sum(cpu)-sum(old_cpu)
    idle = cpu[3]+cpu[4]-old_cpu[3]-old_cpu[4]
    disk = shutil.disk_usage('/')
    net = [0, 0]
    for line in pathlib.Path('/proc/net/dev').read_text().splitlines()[2:]:
        name, values = line.split(':', 1)
        if name.strip() == 'lo' or name.strip().startswith(('docker', 'veth', 'br-')):
            continue
        values = values.split()
        net[0] += int(values[0]); net[1] += int(values[8])
    old_net = previous.get('net', net)
    device=os.stat('/').st_dev; disk_io=None
    for entry in pathlib.Path('/proc/diskstats').read_text().splitlines():
        parts=entry.split()
        if int(parts[0])==os.major(device) and int(parts[1])==os.minor(device):disk_io=[int(parts[i]) for i in (3,5,6,7,9,10)];break
    old_io=previous.get('disk_io') or disk_io
    io_rates={}
    if disk_io and old_io and interval>0:
        operations=disk_io[0]+disk_io[3]-old_io[0]-old_io[3]
        io_rates={'disk_read_bytes_sec':max(0,(disk_io[1]-old_io[1])*512/interval),'disk_write_bytes_sec':max(0,(disk_io[4]-old_io[4])*512/interval),'disk_mean_io_ms':max(0,(disk_io[2]+disk_io[5]-old_io[2]-old_io[5])/operations) if operations>0 else 0}
    report = {'sample_seconds': round(interval, 2), 'host': {
        'cpu_percent': round(100*(1-idle/total), 2) if total > 0 else None,
        'cpus': os.cpu_count(), 'memory_total_bytes': memory['MemTotal'],
        'memory_used_percent': round(100*(1-memory['MemAvailable']/memory['MemTotal']), 2),
        'memory_available_bytes': memory['MemAvailable'],
        'disk_free_bytes': disk.free, 'disk_used_percent': round(100*disk.used/disk.total, 2),
        'network_rx_bytes_sec': max(0, (net[0]-old_net[0])/interval) if interval > 0 else None,
        'network_tx_bytes_sec': max(0, (net[1]-old_net[1])/interval) if interval > 0 else None},
        'containers': [], 'checks': {}, 'issues': []}
    ids = command(['docker', 'ps', '-aq']).split()[:50]
    stats = {}
    for line in command(['docker', 'stats', '--no-stream', '--format', '{{json .}}']).splitlines():
        value = json.loads(line); stats[value['Name']] = value
    runtimes = {}; cgroups={}
    db_target = None
    for cid in ids:
        try:
            info = json.loads(command(['docker', 'inspect', cid]))[0]
        except Exception:
            continue  # containers may disappear during a rolling update
        name = info['Name'].lstrip('/')
        # Exclude ephemeral Coolify build helpers; retain all deployed resources.
        if name.startswith(('coolify-helper', 'buildx_buildkit')):
            continue
        sample = {'name': name, 'state': info['State']['Status'],
                  'health': info['State'].get('Health', {}).get('Status'),
                  'restarts': info['RestartCount'], 'oom_killed': info['State'].get('OOMKilled', False),
                  'memory_limit_bytes': info['HostConfig']['Memory'],
                  'cpu_limit_cores': info['HostConfig']['NanoCpus']/1e9 or None}
        if name in stats:
            st = stats[name]
            sample['memory_limit_used_percent']=float(st['MemPerc'].rstrip('%'))
            sample.update(cpu_cores=round(float(st['CPUPerc'].rstrip('%'))/100, 3),
                          memory=st['MemUsage'], network=st['NetIO'], disk_io=st['BlockIO'])
        cgroup = pathlib.Path('/sys/fs/cgroup/system.slice/docker-'+info['Id']+'.scope/cpu.stat')
        if cgroup.exists():
            sample['cpu_cgroup'] = {k: int(v) for k, v in (line.split() for line in cgroup.read_text().splitlines())}
            cgroups[cid]=sample['cpu_cgroup']
            old=previous.get('cgroups',{}).get(cid,{})
            periods=sample['cpu_cgroup'].get('nr_periods',0)-old.get('nr_periods',0)
            sample['throttled_period_percent']=round(100*max(0,sample['cpu_cgroup'].get('nr_throttled',0)-old.get('nr_throttled',0))/periods,2) if old and periods>0 else None
        # Only known application containers have the aggregate runtime file.
        if name.startswith(('atlxpres-scale-pilot-api-', 'x139kasktvpelpef1t6xgid3-')) and sample['state'] == 'running':
            try:
                runtime = json.loads(command(['docker', 'exec', cid, 'cat', '/tmp/atl-api-metrics.json']))
                if now-runtime.get('exported_unix',0)>90:raise RuntimeError('runtime_metrics_stale')
                runtimes[cid] = runtime
                sample['runtime'] = runtime
                sample['interval'] = runtime_delta(runtime, previous.get('runtimes', {}).get(cid), interval)
            except Exception:
                sample['metrics_unavailable'] = True
        if name.startswith('x139kasktvpelpef1t6xgid3-') and sample['state'] == 'running':
            env = dict(v.split('=', 1) for v in info['Config']['Env'] if '=' in v)
            db_target = ['docker', 'exec', '-i', 'coolify-db', 'psql', env['DATABASE_URL']]
        if role == 'database' and name == 'uhvmflj3j60wmnwpbza1bnaf':
            env = dict(v.split('=', 1) for v in info['Config']['Env'] if '=' in v)
            db_target = ['docker', 'exec', '-i', cid, 'psql', '-U', env.get('POSTGRES_USER', 'postgres'), '-d', 'across_db']
        report['containers'].append(sample)
    report['checks'] = probe_ready()
    report['host'].update(io_rates)
    state = {'disk_io':disk_io, 'time': now, 'cpu': cpu, 'net': net, 'runtimes': runtimes, 'cgroups': cgroups}
    return report, state, db_target


DB_HEALTH = """SELECT json_build_object(
 'full_stats_visible',pg_has_role(current_user,'pg_read_all_stats','MEMBER'),
 'connections',(SELECT count(*) FROM pg_stat_activity),'max_connections',current_setting('max_connections')::int,
 'active',(SELECT count(*) FROM pg_stat_activity WHERE state='active'),
 'lock_waiters',(SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock'),
 'idle_in_transaction',(SELECT count(*) FROM pg_stat_activity WHERE state='idle in transaction'),
 'longest_transaction_seconds',(SELECT COALESCE(max(extract(epoch FROM clock_timestamp()-xact_start)),0) FROM pg_stat_activity WHERE state<>'idle'),
 'peer_monitor_age_seconds',(SELECT COALESCE(extract(epoch FROM clock_timestamp()-max(observed_at)),1000000) FROM ops_capacity_samples WHERE host<>'CURRENT_MONITOR_ROLE'),
 'database_bytes',pg_database_size(current_database()),
 'email_pending',(SELECT count(*) FROM email_outbox WHERE status IN('pending','retry','sending')),
 'email_oldest_seconds',(SELECT COALESCE(extract(epoch FROM clock_timestamp()-min(created_at)),0) FROM email_outbox WHERE status IN('pending','retry','sending')),
 'media_pending',(SELECT count(*) FROM provider_video_uploads WHERE state IN('queued','processing')),
 'media_oldest_seconds',(SELECT COALESCE(extract(epoch FROM clock_timestamp()-min(created_at)),0) FROM provider_video_uploads WHERE state IN('queued','processing')),
 'push_pending',(SELECT count(*) FROM notification_push_deliveries WHERE status IN('pending','retry','sending')),
 'push_oldest_seconds',(SELECT COALESCE(extract(epoch FROM clock_timestamp()-min(n.created_at)),0) FROM notification_push_deliveries d JOIN notifications n ON n.id=d.notification_id WHERE d.status IN('pending','retry','sending')),
 'email_sent_total',(SELECT count(*) FROM email_outbox WHERE status='sent'),
 'video_bytes_total',(SELECT COALESCE(sum(size_bytes),0) FROM provider_post_video_assets));"""


def assess(report, previous):
    """Sustained suggestions, not automatic scaling or invented capacity guarantees."""
    h, db, checks = report['host'], report.get('database', {}), report['checks']
    conditions = {'host_cpu': (h.get('cpu_percent') or 0) >= 75,
                  'host_memory': h['memory_used_percent'] >= 85,
                  'host_disk': h['disk_used_percent'] >= 85,
                  'api_unavailable': not checks.get('api_ready', False) and not checks.get('probe_rejected', False),
                  'api_probe_rejected': checks.get('probe_rejected', False),
                  'database_locks': db.get('lock_waiters', 0) > 0,
                  'peer_monitor_stale': db.get('peer_monitor_age_seconds',0)>180,
                  'email_queue': db.get('email_oldest_seconds', 0) > 120,
                  'push_queue': db.get('push_oldest_seconds', 0) > 120,
                  'media_queue': db.get('media_oldest_seconds', 0) > 600,
                  'database_connections': db.get('connections', 0) >= .8*db.get('max_connections', 100),
                  'database_unavailable': 'database' not in report}
    for container in report['containers']:
        if (container.get('throttled_period_percent') or 0)>10:conditions['container_cpu_limit']=True
        if (container.get('memory_limit_used_percent') or 0)>85:conditions['container_memory_limit']=True
        if container.get('metrics_unavailable'):conditions['api_metrics_missing']=True
        if container.get('oom_killed') or container.get('health') == 'unhealthy':
            conditions['container_unhealthy'] = True
        for group in container.get('interval', {}).get('groups', {}).values():
            if group['requests'] >= 20 and group['server_errors']/group['requests'] > .01:
                conditions['api_errors'] = True
            if group['requests'] >= 20 and (group['p95_upper_ms'] == '>3000' or (group['p95_upper_ms'] or 0) > 1000):
                conditions['api_latency'] = True
    streaks = {key: previous.get(key, 0)+1 if active else 0 for key, active in conditions.items()}
    return [key for key, count in streaks.items() if count >= (2 if key == 'api_unavailable' else 3)], streaks


def send_alert(config, subject, body):
    message=EmailMessage();message['From']=config['from'];message['To']='admin@atlxpres.com'
    message['Subject']=subject;message.set_content(body)
    if int(config['port'])==465:
        client=smtplib.SMTP_SSL(config['host'],465,timeout=8,context=ssl.create_default_context())
    else:
        client=smtplib.SMTP(config['host'],int(config['port']),timeout=8)
        client.ehlo();client.starttls(context=ssl.create_default_context());client.ehlo()
    with client:
        client.login(config['username'],config['password'])
        refused=client.send_message(message)
        if refused:raise RuntimeError('Operational alert not accepted')


def main():
    os.umask(0o077); ROOT.mkdir(mode=0o700, exist_ok=True)
    role = pathlib.Path('/etc/atl-monitor-role').read_text().strip()
    if role not in ('main', 'database'):
        raise SystemExit('Invalid monitoring role')
    mailpath=pathlib.Path('/etc/atl-monitor-mail.json')
    mail=json.loads(mailpath.read_text()) if mailpath.exists() else None
    if '--test-alert' in sys.argv:
        if not mail:raise RuntimeError('Alert email not configured')
        send_alert(mail,'[ATL TEST] Operational monitoring alert', 'This is the single authorized monitoring test. Server and application alerts go to admin@atlxpres.com. No customer notifications or payments were triggered.')
        print('Authorized test alert accepted by SMTP. Inbox delivery must be confirmed by the owner.');return
    previous = json.loads((ROOT/'previous.json').read_text()) if (ROOT/'previous.json').exists() else {}
    report, state, target = collect(role, previous)
    if target:
        target += ['-X', '-qAt', '-v', 'ON_ERROR_STOP=1']
        try:
            report['database'] = json.loads(command(target, "BEGIN READ ONLY;SET LOCAL statement_timeout='3s';"+DB_HEALTH.replace('CURRENT_MONITOR_ROLE',role)+'COMMIT;'))
        except Exception:
            report['issues'].append('database_snapshot_unavailable')
    if report.get('database',{}).get('full_stats_visible') is False:report['issues'].append('database_stats_permission_incomplete')
    report['alerts'], state['streaks'] = assess(report, previous.get('streaks', {}))
    report['observed_utc'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    report['email_alerts_configured']=bool(mail)
    # Record an attempt before SMTP. A timeout after DATA must not cause an email storm.
    old_alerts=previous.get('last_notified_alerts',[])
    state['mail_attempts']=[t for t in previous.get('mail_attempts',[]) if time.time()-t<86400]
    state['last_notified_alerts']=old_alerts;state['active_alerts']=report['alerts'];state['last_mail_attempt']=previous.get('last_mail_attempt',0)
    changed=set(report['alerts'])!=set(old_alerts)
    if mail and changed and len(state['mail_attempts'])>=6:report['issues'].append('operational_email_daily_limit_reached')
    if mail and changed and time.time()-state['last_mail_attempt']>=300 and len(state['mail_attempts'])<6:
        state['last_mail_attempt']=time.time();state['mail_attempts'].append(time.time());atomic(ROOT/'previous.json',state)
        try:
            send_alert(mail,'[ATL '+role+'] '+('Resource alert' if report['alerts'] else 'Recovered'),
                       'Sustained conditions: '+(', '.join(report['alerts']) or 'Previous conditions cleared')+'.\nOpen Admin Overview > Server capacity for measurements. These alerts do not automatically purchase or scale infrastructure.')
            report['email_alert_status']='accepted';state['last_notified_alerts']=report['alerts']
        except Exception:
            report['email_alert_status']='unconfirmed';report['issues'].append('alert_delivery_unconfirmed')
    atomic(ROOT/'latest.json', report); atomic(ROOT/'previous.json', state)
    # Failure to insert is visible in the service journal, and does not erase the local sample.
    if not target:
        raise RuntimeError('monitor_database_target_unavailable')
    payload = json.dumps(report, allow_nan=False, separators=(',', ':')).encode()
    if len(payload) > 60000:
        raise RuntimeError('monitor_payload_bound_exceeded')
    encoded = base64.b64encode(payload).decode()
    sql = "BEGIN;SET LOCAL lock_timeout='1s';SET LOCAL statement_timeout='5s';INSERT INTO ops_capacity_samples(host,report) VALUES ('"+role+"',convert_from(decode('"+encoded+"','base64'),'UTF8')::jsonb) ON CONFLICT(host,observed_at) DO UPDATE SET report=EXCLUDED.report;DELETE FROM ops_capacity_samples WHERE observed_at<clock_timestamp()-interval '7 days';COMMIT;"
    command(target, sql)
    print('Capacity sample saved; active alerts: '+','.join(report['alerts']))


if __name__ == '__main__':
    try:
        main()
    except Exception:
        raise SystemExit('Capacity collection incomplete; inspect local latest.json and retry next minute.')
