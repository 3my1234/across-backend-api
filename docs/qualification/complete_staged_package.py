"""Finish an already uploaded package only; never create containers or start load."""
import argparse,base64,hashlib,json,os,re,stat,tarfile
from pathlib import Path,PurePosixPath

def complete(base,run,role,expected):
    if (base/'manifest.json').exists():raise ValueError('setup already started; refusing package extraction')
    if base.is_symlink() or not base.is_dir():raise ValueError('existing real staged directory required')
    archive=base/'setup.tar'
    if archive.exists():
        with tarfile.open(archive) as stream:
            for item in stream.getmembers():
                path=PurePosixPath(item.name)
                if path.is_absolute() or '..' in path.parts or not (item.isfile() or item.isdir()):raise ValueError('unsafe setup archive member')
            stream.extractall(base,filter='data')
    for relative,digest in expected.items():
        path=PurePosixPath(relative)
        if path.is_absolute() or '..' in path.parts:raise ValueError('invalid expected path')
        file=base/relative
        if file.is_symlink() or hashlib.sha256(file.read_bytes()).hexdigest()!=digest:raise ValueError('staged file mismatch: '+relative)
    request=json.loads((base/'request.json').read_text())
    approval=json.loads((base/'approval.json').read_text())
    if request.get('run')!=run or request.get('role')!=role or approval.get('run')!=run or approval.get('authorized_setup') is not True or approval.get('load_authorized') is not False:raise ValueError('setup-only identity/approval mismatch')
    for file in [base,*base.rglob('*')]:
        if file.is_symlink():raise ValueError('unexpected staged symlink')
        file.chmod(stat.S_IMODE(file.stat().st_mode)&~0o077)
    archive.unlink(missing_ok=True)
    return dict(staged_package='verified',role=role,resources_created=False,uploads_repeated=False)

def main():
    p=argparse.ArgumentParser();p.add_argument('--run',required=True);p.add_argument('--role',choices=['main','database'],required=True);p.add_argument('--expected',required=True);a=p.parse_args()
    if os.geteuid()!=0 or not re.fullmatch('cohost-[a-f0-9]{16}',a.run):raise ValueError('root and fixed nonce required')
    print(json.dumps(complete(Path('/root/atlxpres-cohost')/a.run,a.run,a.role,json.loads(base64.b64decode(a.expected,validate=True)))))

if __name__=='__main__':main()
