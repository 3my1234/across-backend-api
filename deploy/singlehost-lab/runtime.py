"""Single-host lifecycle primitives. No remote hosts, SSH relays or Docker pruning."""
import json,re,subprocess,time
from pathlib import Path
import sys
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'cohost-lab'))
from safety import validate_inventory

LABEL='atlxpres.cohost.run'
ROOT=Path('/root/atlxpres-singlehost')

def active_imports(proc=Path('/proc')):
    found=[]
    for path in proc.glob('[0-9]*/cmdline'):
        try:
            args=path.read_bytes().split(b'\0')
            if args and args[0].split(b'/')[-1]==b'docker' and (args[1:2]==[b'load'] or args[1:3]==[b'image',b'load']):found.append(int(path.parent.name))
        except (OSError,ValueError):pass
    return found

def call(args,timeout=30,**kwargs):
    return subprocess.check_output(args,timeout=timeout,**kwargs)

def identity(run):
    if not re.fullmatch('cohost-[a-f0-9]{16}',run):raise ValueError('fixed nonce required')
    return run.replace('-','')+'single'

def slices(run):
    stem=identity(run)
    return {stem+'.slice':(300,5905580032),stem+'-app.slice':(100,2684354560),stem+'-db.slice':(200,3221225472)}

def install_limits(run):
    # Anchors keep slices alive before Docker creates its first container.
    for name,(cpu,memory) in slices(run).items():
        subprocess.run(['systemd-run','--unit',name.removesuffix('.slice')+'anchor','--slice',name,'/bin/sleep','infinity'],check=True,timeout=15)
        subprocess.run(['systemctl','set-property','--runtime',name,'CPUQuota='+str(cpu)+'%','MemoryMax='+str(memory),'MemorySwapMax=0','CPUWeight=10','IOWeight=10','TasksMax=2048'],check=True,timeout=15)

def compose(base,*args,timeout=120):
    doc=json.loads((base/'compose.runtime.json').read_text())
    return call(['docker','compose','--project-name',doc['name'],'--file',str(base/'compose.runtime.json'),*args],timeout,cwd=base)

def inventory(base,timeout=30):
    doc=json.loads((base/'compose.runtime.json').read_text());run=doc['name'].removesuffix('-singlehost');identity(run)
    # The generator has the same evidence nonce but belongs to a separate
    # workload (and normally a separate host). Inventory only this immutable
    # Compose project as well as its run label, never the generator container.
    ids=call(['docker','ps','-aq','--no-trunc','--filter','label='+LABEL+'='+run,
        '--filter','label=com.docker.compose.project='+doc['name']],timeout=timeout).decode().split()
    rows=json.loads(call(['docker','inspect',*ids],timeout=timeout)) if ids else []
    if ids:validate_inventory(dict(run=run,container_ids=ids),rows)
    names={r['Config']['Labels']['com.docker.compose.service']:r['Id'] for r in rows}
    if len(names)!=len(rows) or not set(names)<=set(doc['services']):raise ValueError('unexpected owned service inventory')
    return dict(run=run,role='database',container_ids=ids,services=names,topology='singlehost'),rows

def exec_lab(base,service,command,destination=None,timeout=900):
    manifest,_=inventory(base)
    from control import LAB_COMMANDS
    if service not in ('seed','exporter','verifier','queue-check','lifecycle') or command not in LAB_COMMANDS:raise ValueError('unreviewed lab command')
    ident=manifest['services'][service]
    args=['docker','exec',ident,'/app/lab',command]
    if destination:
        with destination.open('wb') as out:subprocess.run(args,stdout=out,stderr=subprocess.STDOUT,check=True,timeout=timeout)
    else:return call(args,timeout)

def sql(base,text):
    manifest,_=inventory(base)
    return call(['docker','exec','-i',manifest['services']['postgres'],'psql','-U','lab','-d','capacity_lab','-XAt','-v','ON_ERROR_STOP=1'],input=text.encode()).decode().strip()

def verify_limits(base,snapshot=None):
    manifest,rows=inventory(base) if snapshot is None else snapshot
    for row in rows:
        service=row['Config']['Labels']['com.docker.compose.service'];expected=identity(manifest['run'])+('-db.slice' if service=='postgres' else '-app.slice')
        if row['HostConfig']['CgroupParent']!=expected:raise ValueError('wrong parent cgroup')
        if row['HostConfig']['Memory']<=0 or row['HostConfig']['NanoCpus']<=0:raise ValueError('missing container resource limits')
        if service=='postgres' and (row['HostConfig']['NanoCpus']!=2000000000 or row['HostConfig']['Memory']!=3221225472):raise ValueError('PostgreSQL container differs from approved 2CPU/3GiB limit')
        pid=row['State'].get('Pid',0)
        if pid:
            records=(Path('/proc')/str(pid)/'cgroup').read_text().splitlines()
            unified=[line[3:] for line in records if line.startswith('0::')]
            if len(unified)!=1 or expected not in Path(unified[0]).parts or identity(manifest['run'])+'.slice' not in Path(unified[0]).parts:
                raise ValueError('running container is outside the effective parent slice')
    result={}
    for name,(cpu,memory) in slices(manifest['run']).items():
        # systemd slice nesting follows hyphens in the slice name.
        path=Path('/sys/fs/cgroup')/(identity(manifest['run'])+'.slice')
        if name!=identity(manifest['run'])+'.slice':path/=name
        quota,period=map(int,(path/'cpu.max').read_text().split())
        if quota*100!=cpu*period or int((path/'memory.max').read_text())!=memory or (path/'memory.swap.max').read_text().strip()!='0':raise ValueError('effective aggregate resource limits differ')
        result[name]={f:(path/f).read_text() for f in ('cpu.max','memory.max','memory.swap.max','cpu.stat','memory.current','memory.events')}
    return result

def verify_host_bridge(base):
    """Prove host-to-owned-private-network access BEFORE expensive seeding."""
    import ipaddress,socket
    manifest,rows=inventory(base)
    redis=next(row for row in rows if row['Id']==manifest['services']['redis'])
    networks=redis['NetworkSettings']['Networks']
    if len(networks)!=1:raise ValueError('unexpected Redis networks')
    address=next(iter(networks.values()))['IPAddress']
    if ipaddress.ip_address(address) not in ipaddress.ip_network('172.29.248.0/24'):raise ValueError('unexpected private bridge address')
    with socket.create_connection((address,6379),timeout=2) as connection:
        connection.sendall(b'*1\r\n$4\r\nPING\r\n')
        if connection.recv(64)!=b'+PONG\r\n':raise ValueError('owned Redis bridge check failed')
    return dict(private_host_bridge_verified=True,redis_address=address,published_ports=False)

def capture_evidence(base):
    """Best-effort bounded diagnostics before deleting test containers."""
    destination=base/'public';destination.mkdir(mode=0o700,exist_ok=True)
    began=time.monotonic();errors=[]
    try:
        manifest,rows=inventory(base,timeout=5)
        for name,ident in manifest['services'].items():
            if time.monotonic()-began>20:errors.append('diagnostic budget reached');break
            try:
                with (destination/(name+'.log')).open('wb') as out:subprocess.run(['docker','logs','--tail','2000',ident],stdout=out,stderr=subprocess.STDOUT,check=False,timeout=5)
            except Exception as error:errors.append(name+': '+type(error).__name__)
        try:(destination/'effective-limits.json').write_text(json.dumps(verify_limits(base,snapshot=(manifest,rows))))
        except Exception as error:errors.append('cgroup: '+type(error).__name__)
    except Exception as error:errors.append('inventory: '+type(error).__name__)
    if errors:(destination/'collection-errors.json').write_text(json.dumps(errors))

def observations(base,full=False):
    names=('sample.json','watchdog.jsonl','watchdog-error.json','image-identities.json','baseline.json','supervisor.json','snapshot-restored.json','api-running.json','cleanup.complete','cleanup.finalized')
    report={name:(base/name).read_text() for name in names if (base/name).exists()}
    if not full and 'watchdog.jsonl' in report:report['watchdog.jsonl']='\n'.join(report['watchdog.jsonl'].splitlines()[-10:])
    if full and (base/'public').exists():
        report['final_test_logs']={path.name:path.read_text(errors='replace') for path in (base/'public').iterdir() if path.is_file() and path.suffix in ('.log','.json')}
    if (base/'cleanup.complete').exists() or (base/'watchdog-error.json').exists():return report
    try:
        manifest,_=inventory(base)
        report['containers']=call(['docker','stats','--no-stream','--format','{{json .}}',*manifest['container_ids']]).decode() if manifest['container_ids'] else ''
        report['parent_cgroup']=verify_limits(base)
    except Exception as error:report['live_observation_error']=type(error).__name__
    return report

def cleanup(base):
    # Inspect and validate immutable IDs before deleting any object. No broad prune.
    manifest,_=inventory(base);run=manifest['run'];errors=[]
    for ident in manifest['container_ids']:
        try:subprocess.run(['docker','rm','-f','-v',ident],check=True,timeout=30,stdout=subprocess.DEVNULL)
        except Exception as error:errors.append(str(error))
    for kind in ('network','volume'):
        ids=call(['docker',kind,'ls','-q','--filter','label='+LABEL+'='+run]).decode().split()
        for ident in ids:
            try:
                row=json.loads(call(['docker',kind,'inspect',ident]))[0]
                if row.get('Labels',{}).get(LABEL)!=run:raise ValueError('cleanup ownership changed')
                subprocess.run(['docker',kind,'rm',ident],check=True,timeout=30,stdout=subprocess.DEVNULL)
            except Exception as error:errors.append(str(error))
    for name in reversed(list(slices(run))):
        subprocess.run(['systemctl','stop',name.removesuffix('.slice')+'anchor.service'],check=False,timeout=15,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    if errors:raise RuntimeError('test cleanup incomplete: '+'; '.join(errors))
    (base/'cleanup.complete').write_text(str(time.time()))
