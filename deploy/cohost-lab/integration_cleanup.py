"""Disposable CI-only exact-owned cleanup proof, including volume/network rejection."""
import json,os,secrets,subprocess,tempfile
from pathlib import Path


def call(*args):return subprocess.check_output(args,text=True).strip()

def main():
    assert os.environ.get('GITHUB_ACTIONS')=='true' and os.geteuid()==0,'CI only'
    token=secrets.token_hex(8);run='cohost-'+token
    own_volume=run+'-owned';other_volume=run+'-unrelated';network=run+'-network'
    ids=[]
    try:
        call('docker','volume','create','--label','atlxpres.cohost.run='+run,own_volume)
        call('docker','volume','create',other_volume)
        call('docker','network','create','--internal','--label','atlxpres.cohost.run='+run,network)
        sentinel=call('docker','run','-d','--network','none','-v',other_volume+':/keep','alpine:3.23','sh','-c','echo retained >/keep/marker; sleep 300');ids.append(sentinel)
        owned=call('docker','run','-d','--label','atlxpres.cohost.run='+run,'--network',network,'-v',own_volume+':/data','alpine:3.23','sleep','300');ids.append(owned)
        with tempfile.TemporaryDirectory() as d:
            manifest=Path(d)/'manifest.json'
            data=dict(run=run,role='main',container_ids=[owned],production_container_ids=[sentinel],volumes=[own_volume],networks=[network])
            manifest.write_text(json.dumps(data))
            subprocess.run(['python3',str(Path(__file__).with_name('cleanup.py')),str(manifest),'--execute','--remove-disposable-data'],check=True)
            assert call('docker','inspect','-f','{{.State.Running}}',sentinel)=='true'
            assert call('docker','exec',sentinel,'cat','/keep/marker')=='retained'
            assert subprocess.run(['docker','volume','inspect',own_volume],capture_output=True).returncode!=0
            assert subprocess.run(['docker','network','inspect',network],capture_output=True).returncode!=0
            print('PASS exact owned volume/network cleanup; unrelated data retained',flush=True)
            # Deliberately include unrelated data in a second inventory: reject removal.
            owned2=call('docker','run','-d','--label','atlxpres.cohost.run='+run,'--network','none','alpine:3.23','sleep','300');ids.append(owned2)
            data.update(container_ids=[owned2],volumes=[other_volume],networks=[])
            manifest.write_text(json.dumps(data))
            result=subprocess.run(['python3',str(Path(__file__).with_name('cleanup.py')),str(manifest),'--execute','--remove-disposable-data'])
            assert result.returncode!=0
            assert call('docker','exec',sentinel,'cat','/keep/marker')=='retained'
            print('PASS unrelated volume removal refused; sentinel and data intact',flush=True)
    finally:
        for ident in ids:subprocess.run(['docker','rm','-f',ident],capture_output=True)
        for volume in (own_volume,other_volume):subprocess.run(['docker','volume','rm',volume],capture_output=True)
        subprocess.run(['docker','network','rm',network],capture_output=True)
if __name__=='__main__':main()
