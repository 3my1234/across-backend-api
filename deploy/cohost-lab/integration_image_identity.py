"""Disposable CI-only Docker save/load identity proof; no VPS access."""
import gzip,hashlib,json,os,shutil,subprocess,tarfile,tempfile
from pathlib import Path
from image_identity import identities,resolve

def main():
    assert os.environ.get('GITHUB_ACTIONS')=='true','CI only'
    from stage_host import validate_networks
    builtin=json.loads(subprocess.check_output(['docker','network','inspect','bridge','host','none'],text=True,timeout=10))
    validate_networks(builtin)
    print('Real Docker bridge/host/none network inventory validation passed.')
    with tempfile.TemporaryDirectory() as tmp:
        base=Path(tmp);raw=base/'image.tar';archive=base/'images.tar.gz'
        subprocess.run(['docker','image','save','-o',str(raw),'alpine:3.23'],check=True,timeout=30)
        with tarfile.open(raw) as stream:
            row=json.load(stream.extractfile('manifest.json'))[0]
            config=stream.extractfile(row['Config']).read()
        expected='sha256:'+hashlib.sha256(config).hexdigest()
        with raw.open('rb') as source,gzip.open(archive,'wb',compresslevel=1) as target:shutil.copyfileobj(source,target)
        aliases=identities(archive,hashlib.sha256(archive.read_bytes()).hexdigest(),{expected})
        before=resolve(aliases[expected]);assert before in aliases[expected]
        subprocess.run(['docker','load','-i',str(archive)],check=True,timeout=30)
        after=resolve(aliases[expected]);assert before==after
        print(json.dumps(dict(archive_round_trip='passed',verified_same_content_ids=aliases[expected],resolved=after)))

if __name__=='__main__':main()
