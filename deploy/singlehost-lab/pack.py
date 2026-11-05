"""Freeze an explicitly approved reviewed package; never connects to a server."""
import argparse,hashlib,json,subprocess,tarfile,time
from pathlib import Path

def main():
    p=argparse.ArgumentParser();p.add_argument('review',type=Path);p.add_argument('--approve-placement',action='store_true');a=p.parse_args()
    if not a.approve_placement:raise ValueError('explicit changed-placement approval required')
    review=a.review.resolve();approval=json.loads((review/'approval.json').read_text())
    if approval['authorized'] is not False:raise ValueError('review already consumed; do not create duplicate credentials')
    for relative,expected in json.loads((review/'review-hashes.json').read_text()).items():
        if hashlib.sha256((review/relative).read_bytes()).hexdigest()!=expected:raise ValueError('reviewed file changed: '+relative)
    private=review.parents[1]/'singlehost-private'/review.name;private.mkdir(parents=True,exist_ok=False)
    subprocess.run(['ssh-keygen','-q','-t','ed25519','-N','','-C',review.name,'-f',str(private/'ci')],check=True)
    (review/'ci.pub').write_bytes((private/'ci.pub').read_bytes())
    approval.update(authorized=True,approved_unix=int(time.time()))
    from policy import validate
    validate(approval)
    (review/'approval.json').write_text(json.dumps(approval,indent=2))
    package=private/(review.name+'-singlehost.tar.gz')
    with tarfile.open(package,'w:gz',compresslevel=1) as archive:
        for file in sorted(review.rglob('*')):
            if file.name=='fixture.dump' and approval.get('snapshot_cache_run'):continue
            if file.name=='api.bin' and approval.get('api_cache_run'):continue
            if file.is_file() and file.name not in ('render.json','review-hashes.json'):
                archive.add(file,arcname=file.relative_to(review).as_posix(),recursive=False)
    print(json.dumps(dict(package=str(package),sha256=hashlib.sha256(package.read_bytes()).hexdigest(),private=str(private),run=review.name)))

if __name__=='__main__':main()
