"""Disposable PostgreSQL16 collector test; no application capacity claims."""
import json
import os
from pathlib import Path
import subprocess
import time
import uuid
from transaction_sampler import collect,target


def main():
    if os.environ.get('GITHUB_ACTIONS')!='true' or os.geteuid()!=0:
        raise ValueError('Disposable root CI only')
    run='cohost-'+uuid.uuid4().hex[:16]
    base=Path('/root/atlxpres-singlehost')/run;base.mkdir(mode=0o700,parents=True)
    name=run+'-sampler-pg'
    cid=None;holder=waiter=None
    def sql(text):
        return subprocess.run(['docker','exec','-i',cid,'psql','-XqAt','-v','ON_ERROR_STOP=1','-U','lab','-d','capacity_lab'],input=text,text=True,capture_output=True,timeout=15)
    try:
        cid=subprocess.check_output(['docker','run','-d','--name',name,'--label','atlxpres.cohost.run='+run,
            '--cpus','2','--memory','512m','-e','POSTGRES_DB=capacity_lab','-e','POSTGRES_USER=lab',
            '-e','POSTGRES_PASSWORD=disposable-sampler-only','postgres:16-alpine'],text=True,timeout=120).strip()
        for _ in range(60):
            if subprocess.run(['docker','exec',cid,'pg_isready','-U','lab','-d','capacity_lab'],capture_output=True,timeout=5).returncode==0:break
            time.sleep(.5)
        else:raise RuntimeError('Disposable PostgreSQL not ready')
        (base/'manifest.json').write_text(json.dumps(dict(run=run,services={'postgres':cid})))
        assert sql("CREATE TABLE capacity_lab_identity(marker text); INSERT INTO capacity_lab_identity VALUES('disposable-compose-only'); CREATE TABLE chat_rooms(id int PRIMARY KEY,kind text,state text); INSERT INTO chat_rooms VALUES(1,'town_hall','open');").returncode==0
        assert sql("UPDATE capacity_lab_identity SET marker='wrong-target';").returncode==0
        try:target(base)
        except ValueError:pass
        else:raise AssertionError('Wrong synthetic identity accepted')
        assert sql("UPDATE capacity_lab_identity SET marker='disposable-compose-only';").returncode==0
        ro=sql("SET default_transaction_read_only=on; UPDATE chat_rooms SET state='closed';")
        assert ro.returncode!=0 and sql('SELECT state FROM chat_rooms;').stdout.strip()=='open'
        args=['docker','exec','-i',cid,'psql','-XqAt','-v','ON_ERROR_STOP=1','-U','lab','-d','capacity_lab']
        holder=subprocess.Popen(args,stdin=subprocess.PIPE,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,text=True)
        holder.stdin.write('BEGIN; SELECT id FROM chat_rooms WHERE id=1 FOR UPDATE; SELECT pg_sleep(5); COMMIT;\n');holder.stdin.close()
        time.sleep(.3)
        waiter=subprocess.Popen(args,stdin=subprocess.PIPE,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,text=True)
        waiter.stdin.write("SELECT kind,CASE WHEN false THEN 'closed' ELSE state END FROM chat_rooms WHERE id=1 FOR UPDATE;\n");waiter.stdin.close()
        result=collect(base,3)
        rows=[json.loads(line) for line in (base/'bounded-transaction-diagnostic/samples.jsonl').read_text().splitlines()]
        assert any(a['blocked_by'] and a['statement_phase']=='room_lock' and a['wait_event_type']=='Lock' for r in rows for a in r['activity'])
        assert all('host_cpu_ticks' in r and 'wal' in r for r in rows)
        assert all('query' not in a for r in rows for a in r['activity'])
        assert holder.wait(timeout=15)==0 and waiter.wait(timeout=15)==0
        assert sql("SELECT count(*) FROM pg_stat_activity WHERE datname='capacity_lab' AND pid<>pg_backend_pid();").stdout.strip()=='0'
        print(json.dumps(dict(passed=True,real_postgres16=True,lock_holder_chain_observed=True,
             read_only_verified=True,sampler_connection_closed=True,application_capacity_qualified=False,**result)))
    finally:
        if cid:
            subprocess.run(['docker','rm','-f','-v',cid],check=True,timeout=30)
            if subprocess.check_output(['docker','ps','-aq','--filter','label=atlxpres.cohost.run='+run],text=True,timeout=15).strip():raise RuntimeError('Disposable cleanup incomplete')
            print(json.dumps(dict(cleanup_completed=True,run=run)))


if __name__=='__main__':main()
