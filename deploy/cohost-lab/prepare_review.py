"""Local-only review package creation. Never contacts or modifies a VPS."""
import argparse
from datetime import datetime,timezone,timedelta
import hashlib,json,secrets,shutil,subprocess
from pathlib import Path


def digest(file):
    h=hashlib.sha256()
    with file.open('rb') as stream:
        for block in iter(lambda:stream.read(1024*1024),b''):h.update(block)
    return h.hexdigest()


def main():
    ap=argparse.ArgumentParser();ap.add_argument('bundle',type=Path);ap.add_argument('output',type=Path);ap.add_argument('private',type=Path);ap.add_argument('--metadata-only',action='store_true');a=ap.parse_args()
    metadata=json.loads((a.bundle/'images.json').read_text())
    expected={Path(name).name:sha for sha,name in (x.split(maxsplit=1) for x in (a.bundle/'SHA256SUMS').read_text().splitlines())}
    for name,sha in expected.items():
        if name=='images.tar.gz' and a.metadata_only and not (a.bundle/name).exists():continue
        if digest(a.bundle/name)!=sha:raise ValueError('artifact checksum mismatch')
    run='cohost-'+secrets.token_hex(8);root=a.output/run;root.mkdir(parents=True,exist_ok=False)
    private=a.private/run;private.mkdir(parents=True,exist_ok=False)
    expiry=(datetime.now(timezone.utc)+timedelta(hours=3)).strftime('%Y%m%d%H%M%SZ')
    for kind in ('ci-main','ci-database','main-database'):
        subprocess.run(['ssh-keygen','-q','-t','ed25519','-N','','-C',run+'-'+kind,'-f',str(private/kind)],check=True)
    tools=Path(__file__).resolve().parent
    for role in ('main','database'):
        base=root/role;base.mkdir()
        request=dict(run=run,role=role,images=metadata['images'],image_source=metadata['source'],
            image_archive_sha256=expected['images.tar.gz'],key_expiry_utc=expiry,image_archive_locally_verified=(a.bundle/'images.tar.gz').exists())
        source=base/'request.json';source.write_text(json.dumps(request))
        compose=subprocess.check_output(['node',str(tools/'render.cjs'),str(source)])
        (base/'compose.json').write_bytes(compose);request['compose_sha256']=hashlib.sha256(compose).hexdigest()
        source.write_text(json.dumps(request,indent=2))
        shutil.copytree(tools,base/'tools',ignore=shutil.ignore_patterns('__pycache__','*.pyc'))
        shutil.copy2(tools.parent/'capacity-lab'/'haproxy.cfg',base/'haproxy.cfg')
        for kind in (('ci-main',) if role=='main' else ('ci-database','main-database')):
            shutil.copy2(private/(kind+'.pub'),base/(kind+'.pub'))
        template=dict(run=run,authorized_setup=False,authorized_image_import=False,load_authorized=False,approved_unix=0)
        (base/'approval.template.json').write_text(json.dumps(template,indent=2))
        plan=subprocess.check_output(['python',str(tools/'stage_host.py'),str(base)])
        (base/'setup-plan.json').write_bytes(plan)
        keys=subprocess.check_output(['python',str(tools/'install_keys.py'),str(base)])
        (base/'authorized-keys-review.txt').write_bytes(keys)
    (root/'review.json').write_text(json.dumps(dict(run=run,image_source=metadata['source'],keys_expire_utc=expiry,
        main_compose_sha256=digest(root/'main'/'compose.json'),database_compose_sha256=digest(root/'database'/'compose.json'),
        load_authorized=False,image_archive_sha256=expected['images.tar.gz']),indent=2))
    print(json.dumps(dict(review=str(root),private_keys_location=str(private),load_authorized=False)))

if __name__=='__main__':main()
