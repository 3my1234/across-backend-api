"""Disposable PostgreSQL-only comparison; never accepts a remote database."""
import json,statistics,subprocess,os,time
from pathlib import Path

OLD="SELECT kind,CASE WHEN EXISTS(SELECT 1 FROM town_hall_requests h WHERE h.room_id=chat_rooms.id AND h.engagement_deadline<=clock_timestamp()) THEN 'closed' ELSE state END FROM chat_rooms WHERE id=$1::uuid FOR UPDATE"
NEW="SELECT r.kind,CASE WHEN h.engagement_deadline<=clock_timestamp() THEN 'closed' ELSE r.state END FROM chat_rooms r LEFT JOIN town_hall_requests h ON h.room_id=r.id WHERE r.id=$1::uuid FOR UPDATE OF r"

def main():
    if os.environ.get('GITHUB_ACTIONS')!='true':raise ValueError('disposable GitHub runner only')
    name='atlxpres-room-lock-profile';out=Path('room-lock-evidence');out.mkdir()
    def sql(text):
        return subprocess.check_output(['docker','exec','-i',name,'psql','-U','postgres','-XAtq','-v','ON_ERROR_STOP=1'],input=text.encode(),timeout=90).decode().strip()
    subprocess.run(['docker','run','--name',name,'-d','--network','none','--cpus','1','--memory','3g','--memory-swap','3g','-e','POSTGRES_HOST_AUTH_METHOD=trust','postgres:16-alpine'],check=True)
    try:
        for _ in range(60):
            try:
                if sql('SELECT 1')=='1':break
            except subprocess.CalledProcessError:pass
            time.sleep(1)
        sql("""CREATE TABLE chat_rooms(id uuid PRIMARY KEY,kind text NOT NULL,state text NOT NULL);
CREATE TABLE town_hall_requests(room_id uuid PRIMARY KEY REFERENCES chat_rooms(id),engagement_deadline timestamptz NOT NULL);
INSERT INTO chat_rooms SELECT md5(i::text)::uuid,'town_hall','open' FROM generate_series(1,100050)i;
INSERT INTO town_hall_requests SELECT id,now()+CASE WHEN id::text<'8' THEN interval '-1 day' ELSE interval '1 day' END FROM chat_rooms;
INSERT INTO chat_rooms VALUES(md5('direct')::uuid,'direct','open');
CREATE INDEX town_hall_lifetime_deadline ON town_hall_requests(engagement_deadline,room_id);
ANALYZE;""")
        identifiers=sql("SELECT id FROM chat_rooms ORDER BY id LIMIT 4").splitlines()+sql("SELECT id FROM chat_rooms ORDER BY id DESC LIMIT 4").splitlines()+[sql("SELECT md5('direct')::uuid")]
        for ident in identifiers:
            a=sql(OLD.replace('$1',"'"+ident+"'"));b=sql(NEW.replace('$1',"'"+ident+"'"))
            if a!=b:raise ValueError('room state/expiry result changed')
        result={'rows':100050,'cpu':1,'memory_bytes':3*2**30,'result_equivalence_passed':True,'methods':{},'capacity_qualified':False}
        for mode in ('force_custom_plan','force_generic_plan'):
            samples={'old':[],'candidate':[]};plans={}
            for iteration in range(30):
                for label,query in (('old',OLD),('candidate',NEW)) if iteration%2==0 else (('candidate',NEW),('old',OLD)):
                    ident=identifiers[iteration%len(identifiers)]
                    raw=sql("SET plan_cache_mode="+mode+"; PREPARE tested(uuid) AS "+query+"; EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) EXECUTE tested('"+ident+"');")
                    report=json.loads(raw)[0];samples[label].append(report['Execution Time']);plans[label]=report
            result['methods'][mode]={label:{'median_exec_ms':statistics.median(values),'p95_exec_ms':sorted(values)[int(.95*(len(values)-1))],'max_exec_ms':max(values)} for label,values in samples.items()}
            (out/(mode+'-plans.json')).write_text(json.dumps(plans,indent=2))
        (out/'result.json').write_text(json.dumps(result,indent=2));print(json.dumps(result),flush=True)
    finally:subprocess.run(['docker','rm','-f',name],check=True)

if __name__=='__main__':main()
