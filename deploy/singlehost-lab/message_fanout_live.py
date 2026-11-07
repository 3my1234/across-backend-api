"""Reviewed durable worker overlay for owned synthetic qualification resources only."""
import hashlib,json,time,uuid
from pathlib import Path
from api_artifact import SOURCE
from database_patch import require_target
from message_fanout_replay import PATCH_SHA,LEDGER_SQL,observe,require_ledger,configure
from runtime import sql,call,compose,inventory
WORKER_SHA='f09c2d0902868a09352d1c83701010fc134f158623857a7dab56b0c601ad6f67'

def metadata():
    return dict(source=SOURCE,migration=110,migration_sha256=PATCH_SHA,worker_sha256=WORKER_SHA,worker_max_connections=2,completion_lag_seconds=10,mode='async',isolated_only=True)

def validate(approval):
    if approval.get('message_fanout')!=metadata():raise ValueError('reviewed durable worker approval required')

def validate_files(base):
    from verifier_build import validate_binary
    binary=base/'worker.bin';validate_binary(binary)
    if hashlib.sha256(binary.read_bytes()).hexdigest()!=WORKER_SHA:raise ValueError('reviewed worker executable mismatch')
    raw=Path(__file__).with_name('reviewed-message-fanout.sql').read_bytes().replace(b'\r\n',b'\n')
    if hashlib.sha256(raw).hexdigest()!=PATCH_SHA:raise ValueError('reviewed110 checksum mismatch')
    return raw

def apply(base,approval):
    validate(approval);require_target(base);raw=validate_files(base)
    sql(base,"BEGIN;"+raw.decode()+"\nINSERT INTO schema_migrations(name) VALUES('110_durable_message_notifications.sql');COMMIT;")
    (base/'message-fanout-patch.json').write_text(json.dumps(metadata()))

def start(base,approval):
    validate(approval);require_target(base);validate_files(base)
    compose(base,'start','message-fanout')
    m,rows=inventory(base);ident=m['services']['message-fanout'];row=next(r for r in rows if r['Id']==ident)
    mounts=[v for v in row.get('Mounts',[]) if v.get('Destination')=='/app/worker']
    if row.get('Path')!='/app/worker' or len(mounts)!=1 or mounts[0].get('Source')!=str(base/'worker.bin') or mounts[0].get('RW') is not False:raise ValueError('reviewed worker mount/process mismatch')
    actual=call(['docker','exec',ident,'sha256sum','/proc/1/exe']).decode().split()[0]
    if actual!=WORKER_SHA:raise ValueError('running worker mismatch')
    deadline=time.monotonic()+30
    while True:
        import subprocess
        probe=subprocess.run(['docker','exec',ident,'curl','-fsS','--max-time','2','http://127.0.0.1:9090/api/v1/ready'],capture_output=True,timeout=4)
        if probe.returncode==0:break
        if time.monotonic()>deadline:raise ValueError('durable worker readiness failed')
        time.sleep(.5)
    report=dict(**metadata(),running_worker_verified=True)
    (base/'message-fanout-running.json').write_text(json.dumps(report));return report

def finish(base,approval):
    validate(approval);require_target(base);deadline=time.monotonic()+15
    while True:
        observed=observe(base)
        if observed['failed'] or observed['pending']==0 or time.monotonic()>deadline:break
        time.sleep(.5)
    # The original coordinator rotates the active run UUID after reconciliation.
    # Use its already-uploaded completed summary, never the next stage identity.
    summary=base/'exchange/summary.json'
    stage_run=str(uuid.UUID(json.loads(summary.read_text())['run_id'])) if summary.is_file() else None
    statement=LEDGER_SQL if stage_run is None else LEDGER_SQL.replace('run_id::text',"'"+stage_run+"'::text")
    ledger=json.loads(sql(base,statement));report=dict(**observed,**ledger,stage_run_id=stage_run,external_delivery_tested=False)
    # Save failure evidence before asserting acceptance, including on partial stages.
    (base/'message-fanout-ledger.json').write_text(json.dumps(report))
    require_ledger('async',observed,ledger);return report
