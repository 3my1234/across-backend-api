"""Stop/remove recorded test containers only. Preserve volumes/evidence by default."""
import argparse,json,os
from pathlib import Path
import subprocess
from safety import validate_inventory

def main():
    ap=argparse.ArgumentParser();ap.add_argument('manifest',type=Path);ap.add_argument('--execute',action='store_true');ap.add_argument('--remove-disposable-data',action='store_true');a=ap.parse_args()
    m=json.loads(a.manifest.read_text());ids=m['container_ids']
    records=json.loads(subprocess.check_output(['docker','inspect',*ids],text=True,timeout=10))
    validate_inventory(m,records)
    if set(ids)&set(m.get('production_container_ids',[])):raise ValueError('production ID in test inventory')
    print(json.dumps(dict(only_recorded_test_ids=ids,delete_volumes=False,execute=a.execute)))
    if not a.execute:return
    info=a.manifest.stat()
    if os.name!='posix' or os.geteuid()!=0 or info.st_uid!=0 or info.st_mode&0o022:raise ValueError('protected root inventory required')
    subprocess.run(['docker','stop','--time','2',*ids],check=True,timeout=120)
    subprocess.run(['docker','rm',*ids],check=True,timeout=30)
    if a.remove_disposable_data:
        for kind,objects in [('volume',m.get('volumes',[])),('network',m.get('networks',[]))]:
            for identifier in objects:
                rows=json.loads(subprocess.check_output(['docker',kind,'inspect',identifier],text=True,timeout=10))
                if len(rows)!=1 or rows[0].get('Labels',{}).get('atlxpres.cohost.run')!=m['run']:
                    raise ValueError('refuse unowned disposable data removal')
                if kind=='network' and rows[0].get('Containers'):raise ValueError('network still attached')
                if kind=='volume' and subprocess.check_output(['docker','ps','-aq','--filter','volume='+identifier],text=True).strip():
                    raise ValueError('volume still in use')
                subprocess.run(['docker',kind,'rm',identifier],check=True,timeout=30)
    # No broad prune, image removal, restore, unrelated service or key changes here.
if __name__=='__main__':main()
