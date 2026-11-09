"""Local entry to the existing nonce-bound control allowlist; no loopback SSH."""
import os,runpy,sys
from pathlib import Path
base=Path(sys.argv[1]);command=sys.argv[2]
if os.name!='posix' or os.geteuid()!=0 or base!=Path('/root/atlxpres-cohost')/base.name:raise ValueError('owned Linux host directory required')
sys.path.insert(0,str(base/'tools'))
os.environ['SSH_ORIGINAL_COMMAND']=command
sys.argv=[str(base/'tools/access.py'),str(base)]
runpy.run_path(str(base/'tools/access.py'),run_name='__main__')
