"""Build public, exact-source migration/preflight tools; never reads runtime env."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

root = Path(__file__).resolve().parents[1]
revision = os.environ.get('RELEASE_REVISION', '')
if not re.fullmatch('[a-f0-9]{40}', revision):
    raise SystemExit('STOPPED: exact source revision required')
actual = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
if actual != revision:
    raise SystemExit('STOPPED: checkout/release revision mismatch')
output = root/'.release-artifacts'/'release-toolkit'
output.mkdir(parents=True, exist_ok=False)
env = dict(os.environ, CGO_ENABLED='0', GOOS='linux', GOARCH='amd64')
for command in ('across-config-check', 'across-migrate'):
    package = './cmd/'+('config-check' if command.endswith('config-check') else 'migrate')
    subprocess.run(['go','build','-p','2','-trimpath','-ldflags=-s -w -X across/backend/internal/buildinfo.Revision='+revision,'-o',str(output/command),package],cwd=root,env=env,check=True)
files = sorted((root/'migrations').glob('*.sql'))
if not files or any(p.is_symlink() or p.stat().st_size > 2*1024*1024 for p in files):
    raise SystemExit('STOPPED: invalid migration source set')
(output/'migrations').mkdir()
for source in files:
    shutil.copyfile(source,output/'migrations'/source.name)
checksums = {p.relative_to(output).as_posix():hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(output.rglob('*')) if p.is_file()}
(output/'manifest.json').write_text(json.dumps({'revision':revision,'platform':'linux/amd64','migration_count':len(files),'files':checksums,'scope':'public executable/source artifacts only; no credentials, databases, deployment or migration execution'},indent=2)+'\n')
print(json.dumps({'release_toolkit':str(output.relative_to(root)),'revision':revision,'migration_count':len(files),'manifest_sha256':hashlib.sha256((output/'manifest.json').read_bytes()).hexdigest()}))
