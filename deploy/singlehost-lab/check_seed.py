"""Validate retained seed COMPONENT evidence; never relabel its failed workflow."""
import hashlib,json
from pathlib import Path
SOURCE='3b3dc37c510298cce5b171e1217fddaf4e3a1f94'
DIGEST='c1418b7678bb187aaedd1816abf0659b14fc93d53832dc13aee37660f92b98be'
def check():
    path=Path(__file__).parent/'proofs/seed-41919802581/result.json'
    raw=path.read_bytes().replace(b'\r\n',b'\n')
    if hashlib.sha256(raw).hexdigest()!=DIGEST:raise ValueError('retained seed evidence checksum mismatch')
    data=json.loads(raw)
    if data.get('actor_pairs')!=500 or data.get('scenario')!='large-busy' or data.get('database_cpu')!=1:raise ValueError('wrong full seed envelope')
    if data.get('seed_counts')!={'actors':500,'migration108':1,'identity':'disposable-compose-only'}:raise ValueError('full seed counts/identity mismatch')
    if not 0<data.get('seed_seconds',0)<=720 or data.get('cleanup_completed') is not True:raise ValueError('full seed duration/cleanup gate')
    db=[value for key,value in data['effective_cgroups'].items() if key.endswith('single-db.slice')]
    if len(db)!=1:raise ValueError('database cgroup evidence absent')
    quota,period=map(int,db[0]['cpu.max'].split())
    if quota!=period or int(db[0]['memory.max'])!=3221225472:raise ValueError('unreviewed seed resource ceiling')
    # The subsequent queue preflight failed. It is explicitly NOT a passed
    # complete workflow or application capacity result.
    if data.get('passed') is not False or data.get('capacity_qualified') is not False:raise ValueError('historical workflow scope changed')
    return dict(retained_full_seed_component_passed=True,source=SOURCE,workflow=41919802581,whole_historical_workflow_passed=False,actor_pairs=500,seed_seconds=data['seed_seconds'],application_capacity_qualified=False)
if __name__=='__main__':print(json.dumps(check()))
