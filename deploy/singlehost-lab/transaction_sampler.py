"""Finite, read-only observation of one already-owned synthetic PostgreSQL container.

No setup, application requests, server settings, SQL parameters or query text output.
This companion is not a qualification runner and never authorizes a live test.
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import threading
import time

ROOT=Path('/root/atlxpres-singlehost')
SQL=r"""SET default_transaction_read_only=on;
SELECT json_build_object('utc',extract(epoch FROM clock_timestamp()),
 'activity',COALESCE((SELECT json_agg(row_to_json(a)) FROM (
  SELECT pid,application_name,state,wait_event_type,wait_event,
   extract(epoch FROM xact_start) AS xact_start,
   extract(epoch FROM query_start) AS query_start,
   pg_blocking_pids(pid) AS blocked_by,
   CASE
    WHEN lower(ltrim(query)) LIKE 'commit%' THEN 'commit'
    WHEN lower(ltrim(query)) LIKE 'rollback%' THEN 'rollback'
    WHEN query ILIKE '%FROM chat_rooms%FOR UPDATE%' THEN 'room_lock'
    WHEN query ILIKE 'WITH recipients AS MATERIALIZED%' THEN 'notification_and_triggers'
    WHEN query ILIKE 'INSERT INTO chat_messages%' THEN 'shared_message_insert'
    WHEN query ILIKE 'INSERT INTO town_hall_offers%' THEN 'offer_upsert'
    WHEN query ILIKE 'UPDATE chat_rooms%' THEN 'room_metadata_update'
    WHEN query ILIKE 'UPDATE chat_participants%' THEN 'participant_update'
    WHEN query ILIKE '%FROM chat_participants%' THEN 'membership_read'
    WHEN query ILIKE '%FROM provider_public_identity%' THEN 'provider_eligibility'
    WHEN query ILIKE 'INSERT INTO provider_conversation_messages%' THEN 'private_message_insert'
    WHEN query ILIKE 'WITH inbox AS%' THEN 'inbox'
    ELSE 'other'
   END AS statement_phase
  FROM pg_stat_activity
  WHERE datname=current_database() AND backend_type='client backend'
    AND pid<>pg_backend_pid() ORDER BY pid LIMIT 64
 ) a),'[]'::json),
 'activity_truncated',(SELECT count(*)>64 FROM pg_stat_activity
   WHERE datname=current_database() AND backend_type='client backend' AND pid<>pg_backend_pid()),
 'io_timing_enabled',current_setting('track_io_timing'),
 'wal_io_timing_enabled',current_setting('track_wal_io_timing'),
 'wal',(SELECT json_build_object('records',wal_records,'bytes',wal_bytes,
    'write_ms',wal_write_time,'sync_ms',wal_sync_time) FROM pg_stat_wal));
\watch 0.1
"""


def run(args,**kwargs):
    return subprocess.check_output(args,text=True,timeout=15,**kwargs)


def target(base):
    if os.name!='posix' or os.geteuid()!=0 or base.parent!=ROOT or not re.fullmatch('cohost-[a-f0-9]{16}',base.name):
        raise ValueError('Fixed root-owned test directory required')
    for p in (base,base/'manifest.json'):
        s=p.stat()
        if p.is_symlink() or s.st_uid!=0 or s.st_mode&0o022:raise ValueError('Unsafe test ownership/path')
    manifest=json.loads((base/'manifest.json').read_text())
    if manifest.get('run')!=base.name:raise ValueError('Manifest nonce mismatch')
    cid=manifest.get('services',{}).get('postgres','')
    if not re.fullmatch('[a-f0-9]{64}',cid):raise ValueError('Immutable PostgreSQL ID required')
    row=json.loads(run(['docker','inspect',cid]))[0]
    if row['Id']!=cid or row.get('Config',{}).get('Labels',{}).get('atlxpres.cohost.run')!=base.name or row.get('State',{}).get('Running') is not True:
        raise ValueError('Unowned or stopped PostgreSQL container')
    marker=run(['docker','exec',cid,'psql','-XqAt','-U','lab','-d','capacity_lab','-c',
                "SELECT current_database()||':'||marker FROM capacity_lab_identity;"]).strip()
    if marker!='capacity_lab:disposable-compose-only':raise ValueError('Synthetic database identity required')
    return cid


def command(cid,seconds):
    if not re.fullmatch('[a-f0-9]{64}',cid) or type(seconds) is not int or not 1<=seconds<=180:
        raise ValueError('Immutable target and duration1..180seconds required')
    # The timeout executes INSIDE the owned container: it survives loss of the CLI.
    return ['docker','exec','-i',cid,'timeout','-s','TERM',str(seconds),
            'psql','-XqAt','-v','ON_ERROR_STOP=1','-U','lab','-d','capacity_lab']


def collect(base,seconds):
    cid=target(base);cmd=command(cid,seconds)
    output=base/'bounded-transaction-diagnostic'
    output.mkdir(mode=0o700,exist_ok=False)
    samples=0;size=0
    with (output/'sampler-error.log').open('x') as errors,(output/'samples.jsonl').open('x') as log:
        proc=subprocess.Popen(cmd,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=errors,text=True)
        timer=threading.Timer(seconds+15,proc.kill);timer.start()
        try:
            proc.stdin.write(SQL);proc.stdin.close()
            for line in proc.stdout:
                if not line.lstrip().startswith('{'):continue
                data=json.loads(line)
                data['host_observed_unix']=time.time()
                data['host_cpu_ticks']=list(map(int,Path('/proc/stat').read_text().splitlines()[0].split()[1:9]))
                data['owned_cgroup_cpu']={}
                parent=Path('/sys/fs/cgroup')/(base.name.replace('-','')+'single.slice')
                for suffix in ('single.slice','single-app.slice','single-db.slice'):
                    path=(parent if suffix=='single.slice' else parent/(base.name.replace('-','')+suffix))/'cpu.stat'
                    if path.is_file():data['owned_cgroup_cpu'][suffix]=path.read_text()
                encoded=json.dumps(data)+'\n';size+=len(encoded.encode())
                if size>16*2**20:raise ValueError('Bounded diagnostic output exceeded16MiB')
                log.write(encoded);log.flush();samples+=1
            code=proc.wait(timeout=5)
            if code not in (0,124,143) or samples==0:raise RuntimeError('Sampler failed or produced no samples')
        finally:
            timer.cancel()
            if proc.poll() is None:
                proc.terminate()
                try:proc.wait(timeout=5)
                except subprocess.TimeoutExpired:proc.kill();proc.wait(timeout=5)
    result=dict(run=base.name,read_only_sampling=True,samples=samples,bounded_seconds=seconds,
                application_load_generated=False,statement_parameters_collected=False)
    (output/'result.json').write_text(json.dumps(result,indent=2))
    return result


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('base',type=Path);p.add_argument('--seconds',type=int,default=180);a=p.parse_args()
    command('a'*64,a.seconds)  # Validate the duration before any Docker call.
    print(json.dumps(collect(a.base,a.seconds)))
