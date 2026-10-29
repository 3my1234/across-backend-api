"""Resolve exact archive content across Docker config-ID/manifest-ID stores."""
import hashlib,json,re,subprocess,tarfile

def identities(archive,expected_sha,expected_configs):
    h=hashlib.sha256()
    with archive.open('rb') as source:
        for chunk in iter(lambda:source.read(1024*1024),b''):h.update(chunk)
    if h.hexdigest()!=expected_sha:raise ValueError('reviewed image archive hash mismatch')
    blobs={};index=None;total=0
    with tarfile.open(archive,'r|gz') as stream:
        for item in stream:
            if not item.isfile():continue
            if item.name=='index.json':index=json.load(stream.extractfile(item))
            elif re.fullmatch(r'blobs/sha256/[a-f0-9]{64}',item.name) and item.size<=1024*1024:
                total+=item.size
                if total>32*1024*1024:raise ValueError('archive metadata bound exceeded')
                data=stream.extractfile(item).read()
                if hashlib.sha256(data).hexdigest()!=item.name.rsplit('/',1)[1]:raise ValueError('archive blob hash mismatch')
                blobs['sha256:'+item.name.rsplit('/',1)[1]]=data
    if not index:raise ValueError('reviewed OCI index missing')
    result={config:[config] for config in expected_configs}
    for entry in index['manifests']:
        manifest_digest=entry['digest'];manifest=json.loads(blobs[manifest_digest])
        config=manifest.get('config',{}).get('digest')
        if config not in result:continue
        settings=json.loads(blobs[config])
        if settings.get('os')!='linux' or settings.get('architecture')!='amd64':raise ValueError('unexpected image platform')
        result[config].append(manifest_digest)
    if any(len(candidates)!=2 for candidates in result.values()):raise ValueError('reviewed image missing or ambiguous')
    return result

def resolve(candidates):
    for candidate in candidates:
        result=subprocess.run(['docker','image','inspect',candidate],capture_output=True,text=True,timeout=15)
        if result.returncode:continue
        rows=json.loads(result.stdout)
        if len(rows)!=1 or rows[0].get('Id')!=candidate:raise ValueError('unexpected resolved immutable identity')
        if rows[0].get('Os')!='linux' or rows[0].get('Architecture')!='amd64':raise ValueError('unexpected loaded platform')
        return candidate
    return None
