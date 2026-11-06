"""One reviewed function correction, restricted to the owned synthetic database."""
import hashlib,json,os
from pathlib import Path
from runtime import ROOT,identity,sql

NAME='109_notification_sender_fast_path.sql'
SHA='d5a1d47cef13730de36497e90766956a82efbb94950d88f3fe30d8331b16f397'

def metadata():
    return dict(migration=NAME,sha256=SHA,source='a38e0dd9baac92f3ecfbddc311c1157a97a2e178',regression_run=42301537629,qualification_run=42303009929,isolated_only=True)

def reviewed_sql():
    raw=Path(__file__).with_name('reviewed-notification-fast-path.sql').read_bytes().replace(b'\r\n',b'\n')
    if hashlib.sha256(raw).hexdigest()!=SHA:raise ValueError('Reviewed migration109 checksum mismatch')
    return raw.decode()

def require_target(base):
    identity(base.name)
    if os.name!='posix' or os.geteuid()!=0 or base.parent!=ROOT or base.is_symlink():raise ValueError('Owned Linux test directory required')
    if sql(base,'SELECT marker FROM capacity_lab_identity')!='disposable-compose-only':raise ValueError('Synthetic DB marker required')

def verify(base):
    require_target(base)
    body=reviewed_sql().split('AS $$',1)[1].rsplit('$$;',1)[0].strip()
    actual=sql(base,"SELECT prosrc FROM pg_proc WHERE oid='notification_sender_identity(jsonb)'::regprocedure")
    if actual.strip()!=body:raise ValueError('Running notification function differs from reviewed109')
    if sql(base,"SELECT count(*) FROM schema_migrations WHERE name='109_notification_sender_fast_path.sql'")!='1':raise ValueError('Migration109 ledger missing')
    return dict(**metadata(),run=base.name,function_verified=True)

def apply(base):
    require_target(base)
    patch=reviewed_sql()
    sql(base,"BEGIN;"+patch+"\nINSERT INTO schema_migrations(name) VALUES('109_notification_sender_fast_path.sql') ON CONFLICT DO NOTHING; COMMIT;")
    report=verify(base)
    (base/'database-patch.json').write_text(json.dumps(report,indent=2))
    return report
