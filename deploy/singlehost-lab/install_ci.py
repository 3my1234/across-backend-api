"""Install one restricted test identity on the free generator; no VPS mutations."""
import json,os,sys
from pathlib import Path
from policy import validate
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'cohost-lab'))
from install_ci_bundle import socket_directory,control_path,cleanup

def install(bundle,output):
    approval=validate(bundle['approval']);key=bundle['key'];known=bundle['known_hosts']
    if not isinstance(key,str) or not key.startswith('-----BEGIN OPENSSH PRIVATE KEY-----') or len(key)>16384:raise ValueError('temporary OpenSSH identity required')
    if not isinstance(known,str) or not known.strip() or len(known)>32768:raise ValueError('pinned database host key required')
    output.mkdir(mode=0o700,exist_ok=False)
    for name,text in (('approval.json',json.dumps(approval)),('trial.key',key),('known_hosts',known)):
        (output/name).write_text(text);(output/name).chmod(0o600)
    sockets=socket_directory(output)
    # Both semantic roles use the SAME host and forced key. There is only one
    # connection identity, no DB tunnel, and no cross-host peer lease.
    config=['Host cohost-main cohost-database','  HostName 79.143.176.173','  User root','  BatchMode yes','  IdentitiesOnly yes','  StrictHostKeyChecking yes',
        '  IdentityFile '+str((output/'trial.key').resolve()),'  UserKnownHostsFile '+str((output/'known_hosts').resolve()),
        '  ControlMaster auto','  ControlPath '+control_path(sockets),'  ControlPersist 60','  ConnectTimeout 10','  ServerAliveInterval 5','  ServerAliveCountMax 2','  ExitOnForwardFailure yes']
    (output/'ssh_config').write_text('\n'.join(config)+'\n');(output/'ssh_config').chmod(0o600)

if __name__=='__main__':
    if sys.argv[1]=='--cleanup':cleanup(Path(sys.argv[2]))
    else:install(json.loads(os.environ['SINGLEHOST_TRIAL_BUNDLE']),Path(sys.argv[1]))
