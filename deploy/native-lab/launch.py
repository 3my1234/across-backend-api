"""systemd lifecycle wrapper. Start only a separately prepared, approved run."""
import argparse,json,os,subprocess,sys
from pathlib import Path
from native import load,NativeRunner

def commands(script,config,run,coordinator=None,cleanup=None,peer_task=None,lifetime=7200):
    prefix='atl-native-'+run.removeprefix('cohost-')
    if not 1<=lifetime<=7200:raise ValueError('hard lifetime exceeded')
    common=['systemd-run','--quiet','--property=Type=exec','--property=Restart=no','--property=RuntimeMaxSec='+str(lifetime),'--property=KillMode=control-group','--property=TasksMax=128']
    peer=common+['--unit='+prefix+'-peer','--property=CPUQuota=25%','--property=MemoryMax=128M',*(peer_task or [sys.executable,str(script),'peer',str(config)])]
    task=coordinator or [sys.executable,str(script),'execute',str(config)]
    final=cleanup or [sys.executable,str(script),'cleanup',str(config)]
    # Paths are root-owned fixed paths without spaces or systemd specifier expansion.
    for value in [str(script),str(config),*final]:
        if any(c.isspace() or c=='%' for c in value):raise ValueError('unsupported unit path/argument')
    # Peer failure stops the coordinator; stopping the coordinator stops its peer.
    # The two existing host-local watchdogs/reapers remain outside these units.
    job=common+['--unit='+prefix+'-job','--property=BindsTo='+prefix+'-peer.service','--property=After='+prefix+'-peer.service','--property=CPUQuota=50%','--property=MemoryMax=512M','--property=TimeoutStopSec=420','--property=ExecStopPost='+' '.join(final),'--property=ExecStopPost=/usr/bin/systemctl --no-block stop '+prefix+'-peer.service',*task]
    return prefix,peer,job

def main():
    p=argparse.ArgumentParser();p.add_argument('action',choices=('start','status','stop'));p.add_argument('config',type=Path);p.add_argument('--approve-trial',action='store_true');a=p.parse_args()
    config=load(a.config);prefix,peer,job=commands(Path(__file__).with_name('native.py'),a.config,config['run'])
    if a.action=='status':
        subprocess.run(['systemctl','status','--no-pager',prefix+'-job',prefix+'-peer'],check=False)
        for name in ('progress.json','result.json','native-cleanup.json'):
            path=Path(config['main_base'])/name
            if path.is_file():print(name+': '+path.read_text())
        return
    if a.action=='stop':subprocess.run(['systemctl','stop',prefix+'-job'],check=True);return
    if not a.approve_trial:raise ValueError('starting live-host load requires --approve-trial; preparation is not approval')
    for unit in (prefix+'-peer',prefix+'-job'):
        if subprocess.run(['systemctl','is-active','--quiet',unit]).returncode==0:raise ValueError('existing run active; do not duplicate')
    runner=NativeRunner(config)
    from trial_policy import validate
    validate(runner.approval,config['run'])
    if (runner.base/'cleanup.complete').exists() or (runner.base/'native-started').exists():raise ValueError('closed/previously launched run; do not reuse')
    # Generator plus full app allocation and unchanged2GiB production reserve.
    available=int(next(v.split()[1] for v in Path('/proc/meminfo').read_text().splitlines() if v.startswith('MemAvailable:')))*1024
    required=runner.approval['generator_memory']+runner.approval['main_memory']+2*2**30
    if available<required:raise ValueError('insufficient main-host reserve for generator+API allocation; no load started')
    network=json.loads(subprocess.check_output(['docker','network','inspect',config['network']]))[0]
    if not network.get('Internal') or network.get('Labels',{}).get('atlxpres.cohost.run')!=config['run']:raise ValueError('owned internal generator network required')
    if subprocess.run(['docker','inspect','cohost-generator'],capture_output=True).returncode==0:raise ValueError('existing generator name; do not replace it')
    runner.base.joinpath('native-started').touch(exist_ok=False)
    try:
        subprocess.run(peer,check=True)
        subprocess.run(job,check=True)
    except BaseException:
        subprocess.run([sys.executable,str(Path(__file__).with_name('native.py')),'cleanup',str(a.config)],check=False)
        subprocess.run(['systemctl','stop',prefix+'-peer'],check=False)
        raise
    print(json.dumps({'detached':True,'unit':prefix+'-job','hard_limit_seconds':7200,'load_success_claimed':False}))
if __name__=='__main__':main()
