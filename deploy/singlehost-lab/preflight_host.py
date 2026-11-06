"""Run the new bounded baseline before uploads, using only verified existing test tools."""
import base64,hashlib,json,os,re,signal,subprocess,sys,time
from pathlib import Path
ROOT=Path('/root/atlxpres-singlehost')

def preflight(run,cache,encoded,hashes,sources=None):
    if os.geteuid()!=0 or any(not re.fullmatch('cohost-[a-f0-9]{16}',s) for s in (run,cache)):raise ValueError('fixed root-owned test preflight required')
    base=ROOT/cache
    if not (base/'cleanup.finalized').is_file():raise ValueError('previous cleanup missing')
    ids=subprocess.check_output(['docker','ps','-aq','--filter','label=atlxpres.cohost.run'],text=True,timeout=15).strip()
    if ids:raise ValueError('existing test containers remain; no new setup')
    sources={} if sources is None else sources
    if set(sources)-{'watchdog.py'}:raise ValueError('only reviewed in-memory observer update allowed')
    decoded={name:base64.b64decode(value,validate=True) for name,value in sources.items()}
    if not set(decoded)<=set(hashes):raise ValueError('reviewed source checksum missing')
    for name,expected in hashes.items():
        if name not in ('watchdog.py','safety.py'):raise ValueError('unexpected observer file')
        content=decoded[name] if name in decoded else (base/'src/deploy/cohost-lab'/name).read_bytes()
        if hashlib.sha256(content).hexdigest()!=expected:raise ValueError('reviewed observer source mismatch')
    directory=base/('preflight-'+run);directory.mkdir(mode=0o700,exist_ok=True)
    if directory.is_symlink():raise ValueError('unsafe preflight directory')
    sys.path.insert(0,str(base/'src/deploy/cohost-lab'))
    if 'watchdog.py' in decoded:
        import types
        observer=types.ModuleType('watchdog')
        exec(compile(decoded['watchdog.py'],'<reviewed-watchdog>','exec'),observer.__dict__)
        sys.modules['watchdog']=observer
    namespace={'__name__':'bounded_preflight_baseline'}
    exec(compile(base64.b64decode(encoded,validate=True),'<reviewed-baseline>','exec'),namespace)
    sys.argv=['baseline.py',str(directory/'baseline.json')]
    def expired(*args):raise TimeoutError('pre-upload baseline exceeded420seconds; no uploads or setup started')
    signal.signal(signal.SIGALRM,expired);signal.alarm(420)
    try:namespace['main']()
    finally:signal.alarm(0)
    print(json.dumps(dict(pre_upload_baseline='passed',run=run,resources_created=False)),flush=True)
