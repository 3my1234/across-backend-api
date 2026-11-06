"""Resume one pinned archive over standard SSH; never install or start anything."""
import argparse
import base64
import hashlib
import subprocess
from pathlib import Path

import json
import re


def prefix_hash(path, size):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        remaining = size
        while remaining:
            block = stream.read(min(1024 * 1024, remaining))
            if not block:
                raise ValueError('Prefix exceeds local archive')
            h.update(block)
            remaining -= len(block)
    return h.hexdigest()


def main():
    p = argparse.ArgumentParser()
    p.add_argument('package', type=Path)
    p.add_argument('--run', required=True)
    p.add_argument('--sha256', required=True)
    a = p.parse_args()
    if not re.fullmatch('cohost-[a-f0-9]{16}',a.run) or not re.fullmatch('[a-f0-9]{64}',a.sha256):raise ValueError('Pinned run and SHA256 required')
    size = a.package.stat().st_size
    if prefix_hash(a.package, size) != a.sha256:
        raise ValueError('Pinned archive or offset mismatch')
    query = "import json,hashlib;from pathlib import Path;p=Path('/root/"+a.run+"-singlehost.tar.gz');assert not p.is_symlink();assert not Path('/root/atlxpres-singlehost/"+a.run+"').exists();print(json.dumps(dict(offset=p.stat().st_size if p.is_file() else 0,sha=hashlib.sha256(p.read_bytes()).hexdigest() if p.is_file() else hashlib.sha256(b'').hexdigest())))"
    encoded_query=base64.b64encode(query.encode()).decode()
    command_query='python3 -B -c "import base64;exec(base64.b64decode(\''+encoded_query+'\'))"'
    print('Inspect existing archive prefix only; enter SSH password locally.',flush=True)
    status=subprocess.check_output(['ssh','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=20','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=3','root@79.143.176.173',command_query],text=True,timeout=120)
    partial=json.loads(status)
    a.offset=partial['offset'];a.prefix_sha=partial['sha']
    if not 0<=a.offset<=size:raise ValueError('Remote archive exceeds reviewed size')
    if a.offset==size and a.prefix_sha==a.sha256:
        print('Remote archive checksum already matches; upload skipped.',flush=True)
        return
    # A damaged partial file is replaced from byte zero, never appended blindly.
    offset = a.offset if prefix_hash(a.package, a.offset) == a.prefix_sha else 0
    code = '''
import hashlib,os,sys
from pathlib import Path
run=RUN_VALUE
path=Path('/root')/(run+'-singlehost.tar.gz')
if (Path('/root/atlxpres-singlehost')/run).exists():raise RuntimeError('Already installed; upload refused')
if path.is_symlink():raise RuntimeError('Unsafe upload path')
offset=OFFSET_VALUE
if offset:
 if not path.is_file() or path.stat().st_size!=offset or hashlib.sha256(path.read_bytes()).hexdigest()!=PREFIX_VALUE:raise RuntimeError('Partial archive changed; inspect before resume')
stream=path.open('r+b' if offset else 'wb')
os.chmod(path,0o600)
with stream:
 stream.seek(offset)
 count=offset
 while True:
  block=sys.stdin.buffer.read(1024*1024)
  if not block:break
  if count+len(block)>SIZE_VALUE:raise RuntimeError('Upload exceeds reviewed size')
  stream.write(block);stream.flush();count+=len(block)
  print('Verified-source archive transfer: %d/%d bytes'%(count,SIZE_VALUE),flush=True)
 os.fsync(stream.fileno())
if count!=SIZE_VALUE or hashlib.sha256(path.read_bytes()).hexdigest()!=SHA_VALUE:raise RuntimeError('Incomplete upload retained; no installation performed')
print('Remote complete archive checksum verified.',flush=True)
'''
    for key, value in {'RUN_VALUE': repr(a.run), 'OFFSET_VALUE': str(offset),
                       'PREFIX_VALUE': repr(a.prefix_sha), 'SIZE_VALUE': str(size),
                       'SHA_VALUE': repr(a.sha256)}.items():
        code = code.replace(key, value)
    encoded = base64.b64encode(code.encode()).decode()
    command = 'python3 -B -c "import base64;exec(base64.b64decode(\'' + encoded + '\'))"'
    print(f'Resuming same archive at {offset}/{size} bytes. Enter SSH password locally.', flush=True)
    with a.package.open('rb') as source:
        source.seek(offset)
        # OpenSSH reads the password from the local console, not this data pipe.
        proc = subprocess.Popen(['ssh', '-o', 'StrictHostKeyChecking=yes', '-o',
                                 'ConnectTimeout=20', '-o', 'ServerAliveInterval=10',
                                 '-o', 'ServerAliveCountMax=3', 'root@79.143.176.173', command],
                                stdin=source)
        try:
            result = proc.wait(timeout=1800)
        except BaseException:
            proc.terminate()
            try:proc.wait(timeout=10)
            except subprocess.TimeoutExpired:proc.kill();proc.wait()
            raise
    if result:
        raise SystemExit(result)


if __name__ == '__main__':
    main()
