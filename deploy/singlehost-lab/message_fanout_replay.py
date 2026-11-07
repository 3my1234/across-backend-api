"""Matched disposable-only inline/async replay hooks, never a live installer."""
import copy,hashlib,json,os,time
from pathlib import Path
from runtime import call,compose,inventory,sql
from database_patch import require_target

PATCH_SHA='940ffedc0891863a34f0e6e8b4a1d78520d667df4607c3433e65b1f9c22f53e5'

def mode():
    value=os.environ.get('MESSAGE_FANOUT_REPLAY')
    if value not in ('inline','async'):raise ValueError('explicit inline/async disposable arm required')
    if os.environ.get('GITHUB_ACTIONS')!='true' or os.name!='posix' or os.geteuid()!=0:raise ValueError('disposable CI root only')
    return value

def build_worker(release,base):
    mode()
    call(['docker','run','--rm','--network','none','-e','CGO_ENABLED=0','-e','GOOS=linux','-e','GOARCH=amd64',
        '-v',str(release)+':/release:ro','-v',str(base)+':/out','-w','/release','singlehost-verifier-builder:local',
        'go','build','-buildvcs=false','-trimpath','-ldflags=-s -w -I /lib/ld-musl-x86_64.so.1','-o','/out/worker.bin','./cmd/worker'],timeout=180)
    from verifier_build import validate_binary
    validate_binary(base/'worker.bin');(base/'worker.bin').chmod(0o555)

def configure(doc,base,arm=None):
    arm=mode() if arm is None else arm
    if arm not in ('inline','async'):raise ValueError('explicit inline/async configuration required')
    for name in ('api1','api2'):
        doc['services'][name]['environment']['ASYNC_MESSAGE_NOTIFICATIONS']='true' if arm=='async' else 'false'
    worker=copy.deepcopy(doc['services']['api1'])
    worker.update(command=['/app/worker'],volumes=[str(base/'worker.bin')+':/app/worker:ro'],cpus=0.25,mem_limit='256m',memswap_limit='256m')
    worker.pop('healthcheck',None)
    worker['environment'].update(WORKER_ROLES='message_fanout',DB_MAX_CONNS='2',DB_MIN_CONNS='1',WORKER_HEALTH_ADDR=':9090',DATABASE_URL=worker['environment']['DATABASE_URL'].replace('system-api1','system-message-fanout'))
    doc['services']['message-fanout']=worker

def apply_patch(base):
    mode();require_target(base)
    raw=Path(__file__).with_name('reviewed-message-fanout.sql').read_bytes().replace(b'\r\n',b'\n')
    if hashlib.sha256(raw).hexdigest()!=PATCH_SHA:raise ValueError('migration110 checksum mismatch')
    sql(base,"BEGIN;"+raw.decode()+"\nINSERT INTO schema_migrations(name) VALUES('110_durable_message_notifications.sql');COMMIT;")
    return dict(migration=110,sha256=PATCH_SHA,mode=mode(),applied_to_both_arms=True)

def start_worker(base):
    mode();compose(base,'start','message-fanout')
    m,_=inventory(base);ident=m['services']['message-fanout']
    expected=hashlib.sha256((base/'worker.bin').read_bytes()).hexdigest()
    actual=call(['docker','exec',ident,'sha256sum','/proc/1/exe']).decode().split()[0]
    if actual!=expected:raise ValueError('running worker executable mismatch')
    deadline=time.monotonic()+30
    while True:
        probe=__import__('subprocess').run(['docker','exec',ident,'curl','-fsS','--max-time','2','http://127.0.0.1:9090/api/v1/ready'],capture_output=True,timeout=4)
        if probe.returncode==0:break
        if time.monotonic()>deadline:raise ValueError('message fanout worker readiness failed')
        time.sleep(1)
    return dict(source=__import__('api_artifact').SOURCE,sha256=actual,roles=['message_fanout'],max_connections=2,shared_app_cpu=1)

def observe(base):
    return json.loads(sql(base,"""SELECT json_build_object('jobs',count(*),'pending',count(*) FILTER(WHERE completed_at IS NULL),
'failed',count(*) FILTER(WHERE failed_at IS NOT NULL),'oldest_pending_seconds',coalesce(max(extract(epoch FROM clock_timestamp()-created_at)) FILTER(WHERE completed_at IS NULL),0),
'max_completed_lag_seconds',coalesce(max(extract(epoch FROM completed_at-created_at)),0)) FROM message_notification_jobs"""))

# Independent expected recipient and delivery pairs. No payload/body is exported.
LEDGER_SQL="""
WITH expected AS (
 SELECT DISTINCT j.event_key||':'||r.id::text AS key,r.id AS user_id,j.type,j.title,j.body,j.data,j.created_at
 FROM message_notification_jobs j CROSS JOIN LATERAL unnest(j.recipient_ids) r(id) JOIN users u ON u.id=r.id
), actual AS (SELECT n.* FROM notifications n JOIN expected e ON e.key=n.event_key),
run AS (SELECT 'sessions-'||run_id::text||':%' AS prefix FROM capacity_lab_session_run),
expected_jobs AS (
 SELECT 'shared-chat:'||m.id::text AS key FROM chat_messages m,run WHERE m.body LIKE '%'||run.prefix
 UNION ALL SELECT 'buyer-provider-message:'||m.id::text FROM provider_conversation_messages m,run WHERE m.body LIKE run.prefix AND m.sender_type='provider'
 UNION ALL SELECT 'provider-native:'||e.id::text FROM provider_marketplace_events e,run WHERE e.event_type='conversation_message' AND e.metadata->>'message' LIKE run.prefix
)
SELECT json_build_object(
 'expected_jobs',(SELECT count(*) FROM expected_jobs),
 'missing_jobs',(SELECT count(*) FROM expected_jobs e LEFT JOIN message_notification_jobs j ON j.event_key=e.key WHERE j.event_key IS NULL),
 'expected_notifications',(SELECT count(*) FROM expected),
 'actual_notifications',(SELECT count(*) FROM actual),
 'missing_notifications',(SELECT count(*) FROM expected e LEFT JOIN actual a ON a.event_key=e.key WHERE a.id IS NULL),
 'payload_mismatches',(SELECT count(*) FROM expected e JOIN actual a ON a.event_key=e.key WHERE a.user_id<>e.user_id OR a.type<>e.type OR a.title<>e.title OR a.body<>e.body OR NOT(a.data @> e.data) OR a.created_at<>e.created_at),
 'expected_native',(SELECT count(*) FROM actual a JOIN user_push_tokens t ON t.user_id=a.user_id AND t.disabled_at IS NULL),
 'missing_native',(SELECT count(*) FROM actual a JOIN user_push_tokens t ON t.user_id=a.user_id AND t.disabled_at IS NULL LEFT JOIN notification_push_deliveries d ON d.notification_id=a.id AND d.push_token_id=t.id WHERE d.notification_id IS NULL),
 'expected_web',(SELECT count(*) FROM actual a JOIN web_push_subscriptions s ON s.user_id=a.user_id AND s.disabled_at IS NULL WHERE NOT(a.data ? 'provider_event_id')),
 'missing_web',(SELECT count(*) FROM actual a JOIN web_push_subscriptions s ON s.user_id=a.user_id AND s.disabled_at IS NULL LEFT JOIN web_push_deliveries d ON d.subscription_id=s.id AND d.source_key='notification:'||a.id::text WHERE NOT(a.data ? 'provider_event_id') AND d.id IS NULL))
"""

def require_ledger(arm,observed,ledger):
    if observed['failed'] or observed['pending']:raise ValueError('notification jobs not completely drained')
    if observed['max_completed_lag_seconds']>10:raise ValueError('notification completion lag exceeded unchanged10-second policy')
    if arm=='async':
        if ledger['expected_jobs']<=0 or ledger['expected_notifications']<=0 or ledger['expected_native']<=0:raise ValueError('empty notification fixture/coverage')
        for key in ('missing_jobs','missing_notifications','payload_mismatches','missing_native','missing_web'):
            if ledger[key]:raise ValueError(key+'='+str(ledger[key]))
        if ledger['actual_notifications']!=ledger['expected_notifications']:raise ValueError('notification inventory mismatch')
    elif observed['jobs']!=0:raise ValueError('inline arm unexpectedly queued notifications')

def finish(base,output):
    arm=mode();deadline=time.monotonic()+15
    while True:
        observed=observe(base)
        if observed['failed'] or observed['pending']==0 or time.monotonic()>deadline:break
        time.sleep(0.5)
    ledger=json.loads(sql(base,LEDGER_SQL));report=dict(mode=arm,**observed,**ledger,external_delivery_tested=False,scope='persisted notification/native/web outbox rows; vendor/device delivery not tested')
    (output/'message-fanout-ledger.json').write_text(json.dumps(report,indent=2))
    require_ledger(arm,observed,ledger)
    return report
