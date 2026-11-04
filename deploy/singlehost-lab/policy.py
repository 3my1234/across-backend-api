"""Offline-only authorization contract for the changed placement, not implicit consent."""
import re,sys,time
from pathlib import Path
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'cohost-lab'))
from trial_policy import STAGES

def validate(approval,now=None):
    now=time.time() if now is None else now
    if not re.fullmatch('cohost-[a-f0-9]{16}',approval.get('run','')):raise ValueError('fixed unique run required')
    expected=dict(topology='singlehost-database-vps',host='79.143.176.173',authorized=True,
        spending_cap_usd=0,aggregate_cpu=3,aggregate_memory=5905580032,application_cpu=2,application_memory=2684354560,
        database_cpu=1,database_memory=3221225472,generator_cpu=2,generator_memory=4294967296,lifetime_seconds=7200,
        timing_policy='http-2s-observer-2.5s-v1')
    for key,value in expected.items():
        if type(approval.get(key)) is not type(value) or approval[key]!=value:raise ValueError('unreviewed approval field: '+key)
    if not 0<=now-approval.get('approved_unix',0)<=86400:raise ValueError('current approval required')
    if approval.get('stages')!=[list(stage) for stage in STAGES]:raise ValueError('original stage sequence required')
    for key,size in (('source_commit',40),('compose_sha256',64),('image_archive_sha256',64),('verifier_sha256',64)):
        if not re.fullmatch('[a-f0-9]{'+str(size)+'}',approval.get(key,'')):raise ValueError('immutable identity required: '+key)
    return approval
