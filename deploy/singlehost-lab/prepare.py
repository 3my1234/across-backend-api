"""Create an UNAUTHORIZED review directory; no SSH, keys or live resources."""
import argparse,hashlib,json,os,re,shutil,subprocess,sys,uuid
from pathlib import Path
from policy import STAGES

ARCHIVE_SHA='020bacefeffd12bdfa460c165b43b471f9f987b5da7579bdfd219137ceb2c366'
APP_SOURCE='13d316c62ce8bdd35c1c2fe72bf3d4d357581914'

def main():
    p=argparse.ArgumentParser();p.add_argument('--workspace',type=Path,required=True);p.add_argument('--rehearsal-run',required=True);p.add_argument('--snapshot-directory',type=Path,required=True);p.add_argument('--api-directory',type=Path,required=True);p.add_argument('--reuse-server-snapshot-from');a=p.parse_args()
    if not a.rehearsal_run.isdigit():raise ValueError('completed rehearsal ID required')
    root=Path(__file__).resolve().parents[2];workspace=a.workspace.resolve()
    source=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()
    scope=['deploy/singlehost-lab','deploy/cohost-lab','.github/workflows/singlehost-rehearsal.yml','internal/','migrations/','cmd/api/','cmd/capacity-lab/']
    subprocess.run(['git','diff','--exit-code','HEAD','--',*scope],cwd=root,check=True)
    if subprocess.check_output(['git','ls-files','--others','--exclude-standard','--',*scope],cwd=root,text=True).strip():raise ValueError('uncommitted execution source files present')
    from provenance import verify
    verify(root)
    from api_artifact import validate_files
    artifact=json.loads((a.api_directory/'api-artifact.json').read_text())
    validate_files(a.api_directory,artifact)
    replay=json.loads((a.api_directory/'result.json').read_text())
    running=json.loads((a.api_directory/'api-running.json').read_text())
    if replay.get('passed') is not True or replay.get('cleanup_completed') is not True or replay.get('actual_staged_coordinator_smoke_passed') is not True or running.get('both_running_apis_verified') is not True or any(replay.get(k)!=artifact[k] or running.get(k)!=artifact[k] for k in ('api_source','api_sha256','api_regression_run')):raise ValueError('optimized API running/cleanup rehearsal proof required')
    from snapshot import validate
    fixture=validate(a.snapshot_directory/'fixture.dump',json.loads((a.snapshot_directory/'fixture.json').read_text()))
    proof=json.loads((a.snapshot_directory/'snapshot-proof.json').read_text())
    if proof.get('restore_and_original_smoke_passed') is not True or proof.get('snapshot_sha256')!=fixture['sha256'] or not re.fullmatch('[a-f0-9]{40}',proof.get('source','')) or not str(proof.get('workflow_run','')).isdigit():raise ValueError('successful snapshot restore proof required')
    # Retain the verified data/restore component when only controller code changes.
    # No re-download or re-generation; reject any fixture/application/restore drift.
    subprocess.run(['git','merge-base','--is-ancestor',proof['source'],source],cwd=root,check=True)
    subprocess.run(['git','diff','--exit-code',proof['source'],source,'--','internal/','migrations/','cmd/api/','cmd/capacity-lab/','go.mod','go.sum','deploy/singlehost-lab/snapshot.py','deploy/cohost-lab/render.cjs','deploy/capacity-lab/compose.yml','deploy/session-lab/compose.yml','deploy/notification-lab/compose.yml'],cwd=root,check=True)
    # Snapshot rows/schema do not depend on CPU limits. Allow ONLY the exact
    # newly approved 1->2 database CPU resource change in its original renderer.
    original_renderer=subprocess.check_output(['git','show',proof['source']+':deploy/singlehost-lab/render.cjs'],cwd=root,text=True)
    approved_renderer=original_renderer.replace('doc.services.postgres=db;','doc.services.postgres=db;\n  doc.services.postgres.cpus=2;').replace("s.environment.LAB_DB_CPUS='1'","s.environment.LAB_DB_CPUS='2'")
    approved_renderer=approved_renderer.replace('  delete doc.services.gateway.ports;','  delete doc.services.gateway.ports;\n  if (input.api_override===true) for (const name of [\'api1\',\'api2\']) (doc.services[name].volumes??=[]).push(base+\'/api.bin:/app/api:ro\');')
    if (root/'deploy/singlehost-lab/render.cjs').read_text()!=approved_renderer:raise ValueError('renderer change exceeds approved CPU reallocation; retained snapshot proof cannot be reused')
    metadata=json.loads((workspace/'qualification/cohost-image-bundle-40930181592/images.json').read_text())
    if metadata['source']!=APP_SOURCE:raise ValueError('retained image source mismatch')
    run='cohost-'+uuid.uuid4().hex[:16];review=workspace/'qualification/singlehost-review'/run;review.mkdir(parents=True,exist_ok=False)
    input_file=review/'render.json';input_file.write_text(json.dumps(dict(run=run,images=metadata['images'],verifier_override=True,api_override=True)))
    compose=subprocess.check_output(['node',str(root/'deploy/singlehost-lab/render.cjs'),str(input_file)])
    (review/'compose.json').write_bytes(compose)
    for name in ('fixture.dump','fixture.json','snapshot-proof.json'):shutil.copyfile(a.snapshot_directory/name,review/name)
    for name in ('api.bin','api-artifact.json'):shutil.copyfile(a.api_directory/name,review/name)
    build_env={**os.environ,'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'}
    from verifier_build import build_args,validate_binary
    subprocess.run(build_args(review/'verifier.bin'),cwd=root,env=build_env,check=True)
    validate_binary(review/'verifier.bin')
    sys.path.insert(0,str(root/'deploy/cohost-lab'))
    from trial_policy import client_hashes
    approval=dict(run=run,topology='singlehost-database-vps',host='79.143.176.173',authorized=False,approved_unix=0,
        spending_cap_usd=0,aggregate_cpu=3,aggregate_memory=5905580032,application_cpu=1,application_memory=2684354560,
        database_cpu=2,database_memory=3221225472,generator_cpu=2,generator_memory=4294967296,lifetime_seconds=7200,
        timing_policy='http-2s-observer-2.5s-v1',stages=[list(s) for s in STAGES],source_commit=source,
        compose_sha256=hashlib.sha256(compose).hexdigest(),image_archive_sha256=ARCHIVE_SHA,image_source=APP_SOURCE,
        client_hashes=client_hashes(root),rehearsal_run=a.rehearsal_run,verifier_sha256=hashlib.sha256((review/'verifier.bin').read_bytes()).hexdigest(),snapshot_sha256=fixture['sha256'])
    approval['execution_ref']='experiment/approved-singlehost-'+source[:7]
    approval.update(artifact)
    approval['snapshot_rehearsal_run']=str(proof['workflow_run'])
    if a.reuse_server_snapshot_from:
        if not re.fullmatch('cohost-[a-f0-9]{16}',a.reuse_server_snapshot_from):raise ValueError('fixed cached-snapshot nonce required')
        approval['snapshot_cache_run']=a.reuse_server_snapshot_from
    (review/'approval.json').write_text(json.dumps(approval,indent=2))
    shutil.copyfile(root/'deploy/capacity-lab/haproxy.cfg',review/'haproxy.cfg')
    cohost=('baseline.py','watchdog.py','safety.py','stage_host.py','image_identity.py','import_process.py','control.py','exchange.py','trial_policy.py')
    for folder,names in (('cohost-lab',cohost),('singlehost-lab',('runtime.py','supervise.py','monitor.py','rehearse.py','policy.py','snapshot.py','api_artifact.py','verifier_build.py'))):
        destination=review/'src/deploy'/folder;destination.mkdir(parents=True)
        for name in names:shutil.copyfile(root/'deploy'/folder/name,destination/name)
    shutil.copyfile(root/'deploy/singlehost-lab/install_host.py',review/'install_host.py')
    hashes={str(path.relative_to(review)).replace('\\','/'):hashlib.sha256(path.read_bytes()).hexdigest() for path in review.rglob('*') if path.is_file()}
    (review/'review-hashes.json').write_text(json.dumps(hashes,indent=2))
    print(json.dumps(dict(review=str(review),run=run,source=source,authorized=False,resources_created=False)))

if __name__=='__main__':main()
