"""Install a checksum-reviewed small package, never a production deployment."""
import argparse,hashlib,json,os,re,shutil,subprocess,tarfile,time
from pathlib import Path,PurePosixPath

def extract(package,base):
    total=0
    with tarfile.open(package,'r:*') as archive:
        members=archive.getmembers()
        if len(members)>150:raise ValueError('package file count exceeded')
        for row in members:
            path=PurePosixPath(row.name)
            if path.is_absolute() or '..' in path.parts or '\\' in row.name or ':' in row.name or row.issym() or row.islnk() or not row.isfile():raise ValueError('only explicit regular relative files allowed')
            total+=row.size
            limit=256*2**20 if row.name=='fixture.dump' else 64*2**20 if row.name=='api.bin' else 32*2**20 if row.name=='verifier.bin' else 2*2**20
            if total>300*2**20 or row.size>limit:raise ValueError('reviewed package bounds exceeded')
            target=base/path;target.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
            with target.open('xb') as out:shutil.copyfileobj(archive.extractfile(row),out,1024*1024)
            # HAProxy runs as its image's non-root user. This exact file has
            # no credentials; other package files remain root-only.
            target.chmod(0o644 if row.name=='haproxy.cfg' else 0o555 if row.name=='api.bin' else 0o500 if row.name=='verifier.bin' else 0o600)

def key_line(base,public,expiry):
    parts=public.split()
    if len(parts)<2 or parts[0]!='ssh-ed25519' or not re.fullmatch('[A-Za-z0-9+/=]+',parts[1]):raise ValueError('temporary ed25519 public key required')
    helper=base/'src/deploy/singlehost-lab/supervise.py'
    forced=f'/usr/bin/python3 {helper} access {base}'
    # Same reviewed OpenSSH restrictions as the previous narrow gateway key.
    return f'restrict,port-forwarding,permitopen="172.29.248.10:8080",expiry-time="{expiry}",command="{forced}" '+parts[0]+' '+parts[1]+' '+base.name+'-singlehost-ci'

def reuse_fixture(base,approval):
    cached_run=approval['snapshot_cache_run']
    if not re.fullmatch('cohost-[a-f0-9]{16}',cached_run):raise ValueError('invalid cached snapshot nonce')
    cached_fixture=base.parent/cached_run/'fixture.dump'
    if cached_fixture.resolve()!=cached_fixture or cached_fixture.stat().st_uid!=getattr(os,'geteuid',lambda:0)() or cached_fixture.stat().st_mode&0o022:raise ValueError('unsafe cached snapshot ownership/path')
    from snapshot import validate
    meta=json.loads((base/'fixture.json').read_text())
    if meta['sha256']!=approval['snapshot_sha256']:raise ValueError('cached snapshot approval differs')
    validate(cached_fixture,meta)
    os.link(cached_fixture,base/'fixture.dump')

def main():
    import fcntl
    p=argparse.ArgumentParser();p.add_argument('--package',type=Path,required=True);p.add_argument('--sha256',required=True);p.add_argument('--run',required=True);a=p.parse_args()
    if os.geteuid()!=0 or not re.fullmatch('cohost-[a-f0-9]{16}',a.run):raise ValueError('Linux root and reviewed nonce required')
    rows=json.loads(subprocess.check_output(['ip','-j','address','show'],text=True))
    if not any(addr['local']=='79.143.176.173' for row in rows for addr in row.get('addr_info',[])):raise ValueError('database VPS only')
    if a.package!=Path('/root')/(a.run+'-singlehost.tar.gz') or a.package.is_symlink():raise ValueError('fixed uploaded package required')
    checksum=hashlib.sha256()
    with a.package.open('rb') as stream:
        for block in iter(lambda:stream.read(1024*1024),b''):checksum.update(block)
    if checksum.hexdigest()!=a.sha256:raise ValueError('uploaded package checksum mismatch')
    base=Path('/root/atlxpres-singlehost')/a.run
    base.mkdir(parents=True,mode=0o700,exist_ok=False)
    extract(a.package,base)
    import sys
    sys.path.insert(0,str(base/'src/deploy/singlehost-lab'))
    from supervise import validate_approval
    approval=validate_approval(base)
    if approval.get('snapshot_cache_run'):
        reuse_fixture(base,approval)
    cached=Path('/root/atlxpres-cohost/cohost-6d6c5f9220874022/images.tar.gz')
    if cached.is_symlink() or not cached.is_file():raise ValueError('existing reviewed image archive missing')
    os.link(cached,base/'images.tar.gz')
    (base/'exchange').mkdir(mode=0o700)
    expiry=time.strftime('%Y%m%d%H%M%SZ',time.gmtime(time.time()+10800))
    line=key_line(base,(base/'ci.pub').read_text(),expiry)
    keys=Path('/root/.ssh/authorized_keys')
    if keys.is_symlink():raise ValueError('unexpected authorized_keys symlink')
    lock=keys.with_name('atlxpres-singlehost-key.lock')
    with lock.open('a') as handle:
        fcntl.flock(handle,fcntl.LOCK_EX)
        current=keys.read_text() if keys.exists() else ''
        if a.run in current:raise ValueError('key for this run already installed')
        with keys.open('a') as out:out.write(('' if current.endswith('\n') or not current else '\n')+line+'\n')
        keys.chmod(0o600)
    (base/'installed-key.json').write_text(json.dumps(dict(line=line)));(base/'installed-key.json').chmod(0o600)
    helper=base/'src/deploy/singlehost-lab/supervise.py';unit=a.run.replace('-','')+'singlejob'
    try:
        subprocess.run(['systemd-run','--unit',unit,'--property','RuntimeMaxSec=7200','--property','MemoryMax=256M',
            '--property','CPUWeight=10','--property','IOWeight=10','--property','TimeoutStopSec=150',
            '--property','KillMode=control-group','--property',f'ExecStopPost=/usr/bin/python3 {helper} cleanup {base}',
            '/usr/bin/python3',str(helper),'job',str(base)],check=True,timeout=30)
    except BaseException:
        from supervise import remove_key
        remove_key(base)
        raise
    print(json.dumps(dict(job_installed=True,run=a.run,unit=unit,load_started=False,hard_limit_seconds=7200)))

if __name__=='__main__':main()
