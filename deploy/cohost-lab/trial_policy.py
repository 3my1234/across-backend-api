"""Fixed no-spend cohost trial envelope; offline validation has no side effects."""
import hashlib,json,re,time
STAGES=(('baseline-20','smoke',20,45),('baseline-100','stage',100,180),('gradual-500','gradual',500,180),('sustained-500','sustained',500,1800))
LIFETIME=7200

def validate(approval,run,now=None):
    now=time.time() if now is None else now
    if not re.fullmatch('cohost-[a-f0-9]{16}',run):raise ValueError('invalid trial nonce')
    expected=dict(timing_policy="http-2s-observer-2.5s-v1",run=run,load_authorized=True,fixture_preparation_authorized=True,spending_cap_usd=0,
        main_cpu=2,main_memory=2684354560,database_cpu=3,database_memory=3221225472,
        generator_cpu=2,generator_memory=4294967296,lifetime_seconds=LIFETIME)
    for key,value in expected.items():
        if type(approval.get(key)) is not type(value) or approval.get(key)!=value:raise ValueError('unreviewed approval field: '+key)
    if not 0<=now-approval.get('approved_unix',0)<=86400:raise ValueError('current explicit trial approval required')
    if not re.fullmatch('[a-f0-9]{40}',approval.get('source_commit','')):raise ValueError('exact tested source required')
    if approval.get('stages')!=[list(s) for s in STAGES]:raise ValueError('fixed staged workload required')
    return expected

def client_hashes(root):
    names=('sessions.js','policy.mjs','established.mjs','transport.mjs','advance.mjs')
    return {n:hashlib.sha256((root/'deploy/session-lab'/n).read_text(encoding='utf-8').encode()).hexdigest() for n in names}


def peer_progress(statuses,previous,run):
    # Re-reading a still-fresh observation is not new proof: never refresh leases
    # until BOTH sequences advance. All existing identity and <=3s age gates apply.
    from relay import validate as validate_peers
    validate_peers(statuses,{},run)
    for row in statuses:
        if row['sequence']<previous.get(row['role'],0):raise ValueError('peer sequence regressed')
    if any(row['sequence']==previous.get(row['role'],0) for row in statuses):return None
    return validate_peers(statuses,previous,run)
