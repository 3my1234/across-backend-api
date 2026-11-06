"""Build once, then exercise the production restore path in disposable CI."""
import json,os,subprocess,time
from runtime import inventory,call
from snapshot import build,restore,sql

def exercise(root,base,output,sentinel):
    if os.environ.get('GITHUB_ACTIONS')!='true':raise ValueError('disposable only')
    meta=build(base,output)
    ident=inventory(base)[0]['services']['postgres']
    # Only CI deletes its seed database. Live restore refuses a nonempty target.
    subprocess.run(['docker','exec',ident,'dropdb','-U','lab','capacity_lab'],check=True,timeout=30)
    subprocess.run(['docker','exec',ident,'createdb','-U','lab','capacity_lab'],check=True,timeout=30)
    from watchdog import BLOCKING_URLS,HEALTH_SCOPE
    (base/'supervisor.json').write_text(json.dumps(dict(run=base.name,production_container_ids=[sentinel],deadline_unix=time.time()+7200)))
    (base/'baseline.json').write_text(json.dumps(dict(health_scope=HEALTH_SCOPE,timing_policy='http-2s-observer-2.5s-v1',observed_seconds=310,finished_unix=time.time(),urls={u:dict(successful_samples=60,p95_seconds=.1,observer_p95_seconds=.2) for u in BLOCKING_URLS})))
    wrapper=base/'ci-restore-monitor.py'
    wrapper.write_text('import sys\nfrom pathlib import Path\nsys.path.insert(0,'+repr(str(root/'deploy/singlehost-lab'))+')\nimport monitor\nfrom watchdog import state,BLOCKING_URLS\nmonitor.collect_checks=lambda ids,details: ([(u,True,.1) for u in BLOCKING_URLS],state(ids))\nsys.argv=["monitor",'+repr(str(base))+']\nmonitor.main()\n')
    guard=subprocess.Popen(['python3',str(wrapper)],stdout=(output/'restore-guard.log').open('wb'),stderr=subprocess.STDOUT)
    try:
        deadline=time.monotonic()+15
        while not (base/'local.lease').exists():
            if guard.poll() is not None or time.monotonic()>deadline:raise RuntimeError('restore guard did not start')
            time.sleep(.1)
        from supervise import guard_ready
        # Restore must not overwrite any existing table, even in a synthetic DB.
        sql(base,'CREATE TABLE refuse_overwrite(value int); INSERT INTO refuse_overwrite VALUES(73);')
        try:restore(base,output/'fixture.dump',meta,lambda:guard_ready(base))
        except ValueError as error:
            if 'not empty' not in str(error):raise
        else:raise RuntimeError('nonempty restore target accepted')
        if sql(base,'SELECT value FROM refuse_overwrite')!='73':raise RuntimeError('nonempty target modified')
        sql(base,'DROP TABLE refuse_overwrite')
        # Inject a failure after CREATE TABLE (CI only). --single-transaction
        # must roll back every new schema/data object before the normal retry.
        sql(base,"CREATE FUNCTION public.snapshot_reject() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'injected mid-restore failure'; END$$; CREATE EVENT TRIGGER snapshot_reject ON ddl_command_end WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION public.snapshot_reject();")
        try:restore(base,output/'fixture.dump',meta,lambda:guard_ready(base))
        except RuntimeError as error:
            if 'restore failed' not in str(error):raise
        else:raise RuntimeError('partial restore failure did not abort')
        sql(base,'DROP EVENT TRIGGER snapshot_reject; DROP FUNCTION public.snapshot_reject();')
        if sql(base,"SELECT count(*) FROM pg_tables WHERE schemaname='public'")!='0':raise RuntimeError('failed restore left partial tables')
        result=restore(base,output/'fixture.dump',meta,lambda:guard_ready(base))
        result.update(nonempty_target_refused=True,mid_restore_failure_atomic_rollback=True,restore_with_independent_guard=True)
        (output/'snapshot-restore.json').write_text(json.dumps(result,indent=2))
        return result
    finally:
        if guard.poll() is None:
            guard.terminate()
            try:guard.wait(timeout=10)
            except subprocess.TimeoutExpired:guard.kill();guard.wait(timeout=5)
        (base/'local.lease').unlink(missing_ok=True)
