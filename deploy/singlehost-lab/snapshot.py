"""Synthetic-only PostgreSQL snapshot creation and guarded atomic restoration."""
import hashlib,json,os,re,subprocess,time
from pathlib import Path
from runtime import inventory,call
APP_SOURCE='13d316c62ce8bdd35c1c2fe72bf3d4d357581914'
MAX_BYTES=256*2**20

def digest(path):
    value=hashlib.sha256()
    with Path(path).open('rb') as stream:
        for block in iter(lambda:stream.read(1024*1024),b''):value.update(block)
    return value.hexdigest()

def validate(path,meta,now=None):
    now=time.time() if now is None else now
    if meta.get('image_source')!=APP_SOURCE or meta.get('scenario')!='large-busy' or meta.get('actor_pairs')!=500:raise ValueError('wrong synthetic fixture source/shape')
    if not 0<=now-meta.get('created_unix',0)<=86400:raise ValueError('snapshot must be less than24hours old')
    if not re.fullmatch('[a-f0-9]{64}',meta.get('sha256','')):raise ValueError('snapshot checksum missing')
    if path.is_symlink() or not path.is_file() or not 0<path.stat().st_size<=MAX_BYTES or path.stat().st_size!=meta.get('bytes'):raise ValueError('snapshot size/type mismatch')
    with path.open('rb') as stream:
        if stream.read(5)!=b'PGDMP':raise ValueError('PostgreSQL custom archive required')
    if digest(path)!=meta['sha256']:raise ValueError('snapshot checksum mismatch')
    expected={'capacity_lab_actors':500,'users':1000,'products':5000,'provider_listings':5000,'chat_rooms':100050,'chat_messages':1025000,'provider_conversation_messages':500000,'schema_migrations':108}
    tables=meta.get('database',{}).get('tables',{})
    if any(tables.get(k,{}).get('rows')!=v for k,v in expected.items()):raise ValueError('full500-pair inventory required')
    return meta

def postgres(base):
    manifest,_=inventory(base)
    return manifest,manifest['services']['postgres']

def sql(base,text,timeout=180):
    _,ident=postgres(base)
    return call(['docker','exec','-i',ident,'psql','-U','lab','-d','capacity_lab','-XAtq','-v','ON_ERROR_STOP=1'],input=text.encode(),timeout=timeout).decode().strip()

def database_state(base,query=None):
    # All rows, not a sample: unordered sum of per-row hashes plus exact counts.
    # SHA256 protects the transported archive; this inventory checks restoration.
    raw=(query or sql)(base,"""CREATE TEMP TABLE snapshot_inventory(name text,rows bigint,fingerprint text);
DO $$ DECLARE r record; n bigint; h text; BEGIN
 FOR r IN SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename LOOP
 EXECUTE format('SELECT count(*),coalesce(sum(hashtextextended(to_jsonb(t)::text,0)::numeric),0)::text FROM public.%I t',r.tablename) INTO n,h;
 INSERT INTO snapshot_inventory VALUES(r.tablename,n,h);
 END LOOP; END $$;
SELECT json_build_object('tables',(SELECT json_object_agg(name,json_build_object('rows',rows,'fingerprint',fingerprint)) FROM snapshot_inventory),
'triggers',(SELECT json_agg(s) FROM (SELECT c.relname,t.tgname,t.tgenabled,pg_get_triggerdef(t.oid) definition FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND NOT t.tgisinternal ORDER BY c.relname,t.tgname)s));""",timeout=300)
    return json.loads(raw)

def build(base,output):
    if os.environ.get('GITHUB_ACTIONS')!='true':raise ValueError('build synthetic snapshot only in disposable CI')
    if sql(base,"SELECT marker||':'||scenario FROM capacity_lab_identity")!='disposable-compose-only:large-busy':raise ValueError('synthetic identity required')
    if sql(base,"SELECT count(*) FROM users WHERE email NOT LIKE '%@example.invalid' OR password_hash<>'unusable'")!='0':raise ValueError('only pre-session synthetic accounts allowed')
    if sql(base,"SELECT to_regclass('public.capacity_lab_session_run') IS NULL")!='t':raise ValueError('snapshot must precede session tokens/workload')
    _,ident=postgres(base);path=output/'fixture.dump'
    with path.open('xb') as stream:subprocess.run(['docker','exec',ident,'pg_dump','-U','lab','-Fc','-Z','6','--no-owner','--no-acl','capacity_lab'],stdout=stream,check=True,timeout=300)
    state=database_state(base)
    meta=dict(image_source=APP_SOURCE,scenario='large-busy',actor_pairs=500,created_unix=int(time.time()),sha256=digest(path),bytes=path.stat().st_size,database=state)
    validate(path,meta);(output/'fixture.json').write_text(json.dumps(meta,indent=2))
    return meta

def restore(base,path,meta,guard=lambda:None):
    validate(path,meta);manifest,ident=postgres(base)
    # Restrict the native restore to this immutable, labelled container and a
    # genuinely empty database. Never clean/drop an existing target on live hosts.
    if sql(base,"SELECT current_database()||':'||current_setting('cluster_name')")!='capacity_lab:'+manifest['run']:raise ValueError('wrong target cluster')
    if sql(base,"SELECT count(*) FROM pg_tables WHERE schemaname='public'")!='0':raise ValueError('restore target is not empty')
    guard();started=time.monotonic()
    with path.open('rb') as stream, (base/'snapshot-restore.log').open('wb') as log:
        process=subprocess.Popen(['docker','exec','-i',ident,'pg_restore','-U','lab','-d','capacity_lab','--single-transaction','--exit-on-error','--no-owner','--no-acl'],stdin=stream,stdout=log,stderr=subprocess.STDOUT)
        try:
            while process.poll() is None:
                guard()
                if time.monotonic()-started>720:raise RuntimeError('snapshot restore exceeded original720second preparation bound')
                time.sleep(.25)
            if process.returncode:raise RuntimeError('snapshot restore failed; see retained restore log')
        finally:
            if process.poll() is None:
                process.terminate()
                try:process.wait(timeout=5)
                except subprocess.TimeoutExpired:process.kill();process.wait(timeout=5)
    guard()
    if database_state(base)!=meta['database']:raise ValueError('restored data or live triggers differ from verified snapshot')
    if sql(base,"SELECT marker||':'||scenario FROM capacity_lab_identity")!='disposable-compose-only:large-busy':raise ValueError('restored synthetic identity mismatch')
    sql(base,'ANALYZE',timeout=120);guard()
    result=dict(restore_passed=True,all_table_counts_and_row_fingerprints_match=True,live_triggers_match=True,restore_seconds=time.monotonic()-started,snapshot_sha256=meta['sha256'])
    (base/'snapshot-restored.json').write_text(json.dumps(result));return result
