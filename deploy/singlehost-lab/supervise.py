"""Reviewed single-host job. No live execution without explicit topology approval."""
import argparse,base64,json,os,re,signal,subprocess,sys,time
from pathlib import Path
from runtime import ROOT,identity,inventory,compose,install_limits,verify_limits,verify_host_bridge,cleanup,call

def remove_key(base):
    path=base/'installed-key.json'
    if not path.exists():return
    import fcntl
    line=json.loads(path.read_text())['line'];keys=Path('/root/.ssh/authorized_keys')
    if keys.is_symlink():raise ValueError('unsafe authorized_keys path')
    with keys.with_name('atlxpres-singlehost-key.lock').open('a') as handle:
        fcntl.flock(handle,fcntl.LOCK_EX)
        old=keys.read_text().splitlines();new=[row for row in old if row!=line]
        if old!=new:
            temporary=keys.with_name('authorized_keys.singlehost-next')
            with temporary.open('x') as out:out.write('\n'.join(new)+'\n')
            temporary.chmod(0o600);temporary.replace(keys)


def validate_approval(base):
    identity(base.name)
    if os.name!='posix' or os.geteuid()!=0 or base.parent!=ROOT:raise ValueError('fixed Linux root directory required')
    for path in (base,base/'approval.json',base/'compose.json'):
        info=path.stat()
        if info.st_uid!=0 or info.st_mode&0o022 or path.is_symlink():raise ValueError('unsafe ownership or mode')
    approval=json.loads((base/'approval.json').read_text())
    from policy import validate
    validate(approval)
    if approval['run']!=base.name:raise ValueError('approval nonce mismatch')
    import hashlib
    if hashlib.sha256((base/'compose.json').read_bytes()).hexdigest()!=approval.get('compose_sha256'):raise ValueError('reviewed Compose changed')
    if not re.fullmatch('[a-f0-9]{40}',approval.get('source_commit','')):raise ValueError('immutable source required')
    return approval

def guard_ready(base):
    lease=base/'local.lease'
    if (base/'cleanup.complete').exists() or (base/'watchdog-error.json').exists() or not lease.exists() or not 0<=time.time()-lease.stat().st_mtime<=5:raise RuntimeError('independent local safety guard not fresh')

def write_json(path,data):
    tmp=path.with_suffix('.next');tmp.write_text(json.dumps(data));tmp.chmod(0o600);tmp.replace(path)

def check_machine():
    # Refuse accidental installation on the main VPS or an unrelated Linux host.
    rows=json.loads(call(['ip','-j','address','show']))
    addresses={a['local'] for row in rows for a in row.get('addr_info',[])}
    if '79.143.176.173' not in addresses:raise ValueError('reviewed database VPS address absent')

def job(base):
    started=time.time()
    approval=validate_approval(base);check_machine()
    if (base/'supervisor.json').exists() or (base/'cleanup.complete').exists():raise ValueError('refuse duplicate/closed execution')
    from stage_host import validate_networks
    from image_identity import identities,resolve
    from import_process import load_archive
    if call(['docker','info','--format','{{.CgroupDriver}}/{{.CgroupVersion}}']).decode().strip()!='systemd/2':raise ValueError('systemd cgroup v2 required')
    stale=call(['docker','ps','-aq','--filter','label=atlxpres.cohost.run']).decode().split()
    if stale:raise ValueError('existing test containers remain; cleanup first')
    production=call(['docker','ps','-q','--no-trunc']).decode().split()
    if not production:raise ValueError('production inventory empty')
    networks=call(['docker','network','ls','-q']).decode().split()
    validate_networks(json.loads(call(['docker','network','inspect',*networks])) if networks else [])
    # Single five-minute baseline, before imports/container creation.
    subprocess.run(['python3',str(Path(__file__).parents[1]/'cohost-lab/baseline.py'),str(base/'baseline.json')],check=True,timeout=420)
    doc=json.loads((base/'compose.json').read_text())
    required={service['image'] for service in doc['services'].values()}
    aliases=identities(base/'images.tar.gz',approval['image_archive_sha256'],required)
    resolved={image:resolve(aliases[image]) for image in sorted(required)}
    # Inventory needs the reviewed runtime document even while it contains zero services.
    write_json(base/'compose.runtime.json',doc)
    write_json(base/'supervisor.json',dict(run=base.name,production_container_ids=production,deadline_unix=started+7200,phase='image-preparation'))
    monitor=subprocess.Popen(['python3',str(Path(__file__).with_name('monitor.py')),str(base)])
    try:
        deadline=time.monotonic()+15
        while not (base/'local.lease').exists():
            if monitor.poll() is not None or time.monotonic()>deadline:raise RuntimeError('local watchdog failed to start')
            time.sleep(.2)
        guard_ready(base)
        if any(value is None for value in resolved.values()):
            with (base/'image-import.log').open('w') as log:load_archive(base/'images.tar.gz',log,lambda:guard_ready(base))
            resolved={image:resolve(aliases[image]) for image in required}
            if any(value is None for value in resolved.values()):raise RuntimeError('verified archive images unresolved after import')
        for service in doc['services'].values():service['image']=resolved[service['image']]
        write_json(base/'compose.runtime.json',doc);write_json(base/'image-identities.json',resolved)
        install_limits(base.name);guard_ready(base)
        try:compose(base,'create','--no-build','--pull','never')
        finally:
            m,_=inventory(base);write_json(base/'manifest.json',m)
        write_json(base/'effective-limits.json',verify_limits(base))
        compose(base,'start','postgres','redis')
        from rehearse import wait_ready
        wait_ready(base);write_json(base/'private-bridge.json',verify_host_bridge(base));guard_ready(base)
        metadata=json.loads((base/'supervisor.json').read_text());metadata['phase']='waiting-for-generator';write_json(base/'supervisor.json',metadata)
        (base/'setup.ready').write_text(str(time.time()))
        print(json.dumps(dict(singlehost_setup='ready',run=base.name,load_started=False)),flush=True)
        while not (base/'cleanup.requested').exists():
            guard_ready(base)
            if monitor.poll() is not None:raise RuntimeError('local watchdog stopped')
            time.sleep(1)
    finally:
        if monitor.poll() is None:
            monitor.terminate()
            try:monitor.wait(timeout=10)
            except subprocess.TimeoutExpired:monitor.kill();monitor.wait(timeout=5)
        cleanup(base)

def access(base,command):
    approval=validate_approval(base)
    if command=='setup-status':
        metadata=json.loads((base/'supervisor.json').read_text()) if (base/'supervisor.json').exists() else {}
        print(json.dumps(dict(run=base.name,ready=(base/'setup.ready').exists(),closed=(base/'cleanup.complete').exists(),guard_error=(base/'watchdog-error.json').exists(),phase=metadata.get('phase','baseline'))));return
    if command=='cleanup':
        (base/'cleanup.requested').touch()
        deadline=time.monotonic()+150
        while not (base/'cleanup.finalized').exists():
            if time.monotonic()>deadline:raise RuntimeError('job cleanup not confirmed')
            time.sleep(1)
        print('Single-host cleanup confirmed');return
    guard_ready(base)
    if not (base/'setup.ready').exists():raise RuntimeError('setup incomplete')
    metadata=json.loads((base/'supervisor.json').read_text())
    if time.time()>=metadata['deadline_unix']:raise RuntimeError('hard deadline reached')
    m,_=inventory(base)
    if command=='status':
        print(json.dumps(dict(run=base.name,topology='singlehost',age_seconds=time.time()-(base/'local.lease').stat().st_mtime,ready=True)));return
    if command=='activate':
        if (base/'trial-started').exists():raise ValueError('trial already activated')
        with (base/'trial-started').open('x') as out:out.write(str(time.time()))
        print('single-host trial activated');return
    if not (base/'trial-started').exists():raise ValueError('trial not activated')
    if command.startswith('control '):
        encoded=command[8:]
        if len(encoded)>100000:raise ValueError('oversized control request')
        op=json.loads(base64.b64decode(encoded,validate=True))
        m.update(role='database' if op.get('service')=='postgres' else 'main',load_authorized=True,fixture_preparation_authorized=True)
        from control import command as docker_command
        args=docker_command(m,op)
        if op.get('action')=='exec' and op.get('service')=='postgres':
            from runtime import sql
            if sql(base,'SELECT marker FROM capacity_lab_identity')!='disposable-compose-only':raise ValueError('synthetic DB identity missing')
        raise SystemExit(subprocess.run(args,timeout=2500).returncode)
    m.update(role='main',load_authorized=True)
    write_json(base/'exchange-manifest.json',m)
    if command=='gateway-relay':
        # Gateway is reached by SSH through the host's own private bridge.
        print('private gateway ready; no relay or published port');return
    if command=='publish-fixture':
        from runtime import exec_lab
        raw=exec_lab(base,'exporter','export');data=json.loads(raw)
        if data.get('marker')!='disposable-compose-only' or data.get('origin')!='http://gateway:8080':raise ValueError('synthetic fixture required')
        (base/'exchange/fixtures.json').write_bytes(raw);data.pop('sessions',None)
        write_json(base/'exchange/run.json',data);print('synthetic fixture published');return
    if command.startswith('download '):
        from exchange import get
        get(base,command[9:],sys.stdout.buffer);return
    if command.startswith('upload '):
        from exchange import path_for
        name=command[7:];path,limit=path_for(base,name,True);raw=sys.stdin.buffer.read(limit+1)
        if len(raw)>limit:raise ValueError('oversized evidence upload')
        if name=='summary.json' and not json.loads(raw).get('run_id'):raise ValueError('summary identity absent')
        if name=='k6-exit-code.txt' and raw.strip() not in (b'0',b'1',b'2',b'99',b'255',b'130',b'143'):raise ValueError('invalid generator result')
        if path.is_symlink():raise ValueError('unsafe evidence destination')
        temporary=path.with_suffix('.incoming')
        with temporary.open('xb') as out:out.write(raw)
        temporary.replace(path);return
    if command in ('observations','observations-full'):
        report={name:(base/name).read_text() for name in ('sample.json','watchdog.jsonl','watchdog-error.json','image-identities.json') if (base/name).exists()}
        if command=='observations' and 'watchdog.jsonl' in report:report['watchdog.jsonl']='\n'.join(report['watchdog.jsonl'].splitlines()[-10:])
        report['parent_cgroup']=verify_limits(base)
        report['containers']=call(['docker','stats','--no-stream','--format','{{json .}}',*m['container_ids']]).decode()
        print(json.dumps(report));return
    raise ValueError('command denied')

def main():
    p=argparse.ArgumentParser();p.add_argument('action',choices=('job','cleanup','access'));p.add_argument('base',type=Path);a=p.parse_args();base=a.base.resolve()
    if a.action=='job':job(base)
    elif a.action=='cleanup':
        validate_approval(base)
        try:
            if (base/'compose.runtime.json').exists():cleanup(base)
        finally:remove_key(base)
        (base/'cleanup.finalized').write_text(str(time.time()))
    else:access(base,os.environ.get('SSH_ORIGINAL_COMMAND',''))

if __name__=='__main__':main()
