"""Keep application runtime identical; explicitly enumerate test-verifier changes."""
import subprocess
from pathlib import Path
APP_SOURCE='13d316c62ce8bdd35c1c2fe72bf3d4d357581914'
def verify(root):
    prefix=['git','-c','safe.directory='+str(root)]
    subprocess.run(prefix+['diff','--exit-code',APP_SOURCE,'HEAD','--','internal/','migrations/','cmd/api/'],cwd=root,check=True)
    changed=set(subprocess.check_output(prefix+['diff','--name-only',APP_SOURCE,'HEAD','--','cmd/capacity-lab/'],cwd=root,text=True).splitlines())
    allowed={'cmd/capacity-lab/recipient_check.go','cmd/capacity-lab/recipient_check_test.go'}
    if not changed<=allowed:raise ValueError('unreviewed application-tool change: '+str(sorted(changed-allowed)))
    return sorted(changed)
if __name__=='__main__':print(verify(Path(__file__).resolve().parents[2]))
