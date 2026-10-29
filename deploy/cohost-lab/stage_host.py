"""Reviewed SETUP ONLY. No seeding, API startup or application load.
Default prints commands. --execute additionally requires a matching approval file.
Never builds or pulls. Imports only the exact reviewed archive when separately approved.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time


def plan(request, compose):
    run=request.get("run","");role=request.get("role")
    if not re.fullmatch(r"cohost-[a-f0-9]{16}",run) or role not in ("main","database"):
        raise ValueError("invalid run or role")
    if compose.get("name")!=run+"-"+role:raise ValueError("compose project mismatch")
    parent=run.replace("-","")+role+".slice"
    for service in compose["services"].values():
        if service.get("labels",{}).get("atlxpres.cohost.run")!=run:raise ValueError("unowned service")
        if service.get("cgroup_parent")!=parent or service.get("build"):raise ValueError("unbounded service")
        if not re.fullmatch(r"(?:[a-zA-Z0-9./:_-]+@)?sha256:[a-f0-9]{64}",service.get("image","")):
            raise ValueError("unlocked image")
    anchor=run.replace("-","")+role+"anchor.service"
    commands=[
        ["systemd-run","--unit",anchor,"--slice",parent,"/bin/sleep","infinity"],
        ["systemctl","set-property","--runtime",parent,"CPUQuota="+("200%" if role=="main" else "100%"),
         "MemoryMax="+("2684354560" if role=="main" else "3221225472"),"MemorySwapMax=0","CPUWeight=10","IOWeight=10",
         "TasksMax="+("2048" if role=="main" else "512")],
        ["docker","compose","-p",compose["name"],"-f","compose.json","create","--no-build","--pull","never"]]
    # Only idle test PG or test Redis are started. No API, migration, seed or worker.
    commands.append(["docker","compose","-p",compose["name"],"-f","compose.json","start","postgres" if role=="database" else "redis"])
    return commands


def main():
    ap=argparse.ArgumentParser();ap.add_argument("directory",type=Path);ap.add_argument("--execute",action="store_true");a=ap.parse_args()
    base=a.directory.resolve();request=json.loads((base/"request.json").read_text());raw=(base/"compose.json").read_bytes()
    if hashlib.sha256(raw).hexdigest()!=request.get("compose_sha256"):raise ValueError("reviewed compose hash mismatch")
    compose=json.loads(raw);commands=plan(request,compose)
    if not a.execute:
        print(json.dumps(dict(setup_only=True,commands=commands),indent=2));return
    if os.name!="posix" or os.geteuid()!=0:raise ValueError("Linux root required")
    if base!=Path('/root/atlxpres-cohost')/request['run']:raise ValueError("fixed new test directory required")
    approval=json.loads((base/"approval.json").read_text())
    if approval.get("authorized_setup") is not True or approval.get("load_authorized") is not False or approval.get("run")!=request["run"]:
        raise ValueError("explicit matching setup-only approval required")
    if not 0<=time.time()-approval.get("approved_unix",0)<=86400:raise ValueError("current approval required")
    for file in (base,base/'compose.json',base/'request.json',base/'approval.json'):
        info=file.stat()
        if info.st_uid!=0 or info.st_mode&0o022:raise ValueError("unsafe input ownership/mode")
    if (base/"manifest.json").exists():raise ValueError("refuse to restage existing run")
    def capture(args):return subprocess.check_output(args,cwd=base,text=True,timeout=30).strip()
    if capture(["docker","info","--format","{{.CgroupDriver}}/{{.CgroupVersion}}"] )!="systemd/2":
        raise ValueError("required resource-control backend unavailable")
    existing=capture(["docker","ps","-aq","--filter","label=atlxpres.cohost.run="+request['run']])
    if existing:raise ValueError("nonce already has containers")
    import socket,ipaddress
    port=58080 if request['role']=='main' else 55432
    with socket.socket() as sock:
        sock.settimeout(1)
        if sock.connect_ex(('127.0.0.1',port))==0:raise ValueError('test port occupied; do not change existing service')
    if request['role']=='main':
        networks=capture(['docker','network','ls','-q']).splitlines()
        rows=json.loads(capture(['docker','network','inspect',*networks])) if networks else []
        target=ipaddress.ip_network('172.29.248.0/24')
        for row in rows:
            for config in row.get('IPAM',{}).get('Config',[]):
                subnet=config.get('Subnet')
                if subnet:
                    network=ipaddress.ip_network(subnet)
                    if network.version==4 and network.overlaps(target):raise ValueError('test subnet collision')
    production=capture(['docker','ps','-q','--no-trunc']).splitlines()
    if not production:raise ValueError("production inventory unexpectedly empty")
    # A separate setup-only watchdog protects production during NEW resource creation.
    from baseline import validate as validate_baseline
    validate_baseline(json.loads((base/'baseline.json').read_text()),time.time())
    initial=dict(run=request['run'],role=request['role'],container_ids=[],services={},production_container_ids=production,load_authorized=False)
    (base/'manifest.json').write_text(json.dumps(initial));(base/'manifest.json').chmod(0o600)
    guard=request['run'].replace('-','')+request['role']+'guard.service'
    subprocess.run(['systemd-run','--unit',guard,'--property','MemoryMax=128M','--property','CPUQuota=25%',
        '/usr/bin/python3',str(base/'tools'/'watchdog.py'),str(base/'manifest.json'),'--setup-only','--stop-test'],check=True,timeout=10)
    for _ in range(50):
        if (base/'local.lease').exists() and time.time()-(base/'local.lease').stat().st_mtime<5:break
        time.sleep(.2)
    else:raise ValueError('setup watchdog not healthy; no resources created')
    # Import only the exact reviewed archive, under the independent safety guard.
    from image_identity import identities,resolve
    archive=base/'images.tar.gz'
    aliases=identities(archive,request.get('image_archive_sha256'),set(s['image'] for s in compose['services'].values()))
    resolved={}
    missing=[]
    for service in compose['services'].values():
        image=service['image']
        resolved[image]=resolve(aliases[image])
        if resolved[image] is None:missing.append(image)
    if missing:
        if approval.get('authorized_image_import') is not True:raise ValueError('reviewed image import approval required')
        archive=base/'images.tar.gz'
        hasher=hashlib.sha256()
        with archive.open('rb') as stream:
            for chunk in iter(lambda:stream.read(1024*1024),b''):hasher.update(chunk)
        checksum=hasher.hexdigest()
        if checksum!=request.get('image_archive_sha256'):raise ValueError('image archive hash mismatch')
        import gzip
        import_log=(base/'image-import.log').open('w')
        importer=subprocess.Popen(['docker','load'],stdin=subprocess.PIPE,stdout=import_log,stderr=import_log)
        try:
            with gzip.open(archive,'rb') as source:
                while chunk:=source.read(1024*1024):
                    if not (base/'local.lease').exists() or time.time()-(base/'local.lease').stat().st_mtime>5:
                        raise ValueError('setup safety guard lost during image import')
                    importer.stdin.write(chunk)
            importer.stdin.close()
            if importer.wait(timeout=60)!=0:raise ValueError('image import failed')
        finally:
            if importer.poll() is None:importer.terminate();importer.wait(timeout=10)
            import_log.close()
        for image in resolved:
            resolved[image]=resolve(aliases[image])
            if resolved[image] is None:raise ValueError('exact archived image unavailable after load; inspect image-import.log')
    # Both IDs are cryptographically tied to the SAME verified archive content.
    # Change images only in a separate runtime file; reviewed caps/env stay intact.
    runtime=json.loads(raw)
    for service in runtime['services'].values():service['image']=resolved[service['image']]
    (base/'compose.runtime.json').write_text(json.dumps(runtime,indent=2))
    (base/'image-identities.json').write_text(json.dumps(resolved,indent=2))
    for command in commands:
        if 'compose.json' in command:command[command.index('compose.json')]='compose.runtime.json'
    # Docker daemon image-load work is monitored, not constrained by the CLI's cgroup.
    # Record all newly created IDs even if Compose subsequently fails; no broad cleanup.
    try:
        for command in commands[:3]:
            if not (base/'local.lease').exists() or time.time()-(base/'local.lease').stat().st_mtime>5:
                raise ValueError('setup watchdog lost; do not create more resources')
            subprocess.run(command,cwd=base,check=True,timeout=120)
    finally:
        ids=capture(['docker','ps','-aq','--no-trunc','--filter','label=atlxpres.cohost.run='+request['run']]).splitlines()
        services={}
        if ids:
            for row in json.loads(capture(['docker','inspect',*ids])):
                if row['Config']['Labels'].get('atlxpres.cohost.run')!=request['run']:raise ValueError('ownership mismatch')
                services[row['Config']['Labels']['com.docker.compose.service']]=row['Id']
        volumes=capture(['docker','volume','ls','-q','--filter','label=atlxpres.cohost.run='+request['run']]).splitlines()
        networks=capture(['docker','network','ls','-q','--no-trunc','--filter','label=atlxpres.cohost.run='+request['run']]).splitlines()
        manifest=dict(run=request['run'],role=request['role'],container_ids=ids,services=services,volumes=volumes,networks=networks,
                      production_container_ids=production,load_authorized=False)
        pending=base/'manifest.next';pending.write_text(json.dumps(manifest,indent=2));pending.chmod(0o600);os.replace(pending,base/'manifest.json')
    if not (base/'local.lease').exists() or time.time()-(base/'local.lease').stat().st_mtime>5:raise ValueError('setup guardian lost; refuse startup')
    subprocess.run(commands[3],cwd=base,check=True,timeout=30)
    print(json.dumps(dict(setup='created',application_load_started=False,container_count=len(ids))))

if __name__=='__main__':main()
