"""Install operator-supplied temporary restricted keys on the free runner only."""
import json,os,re,sys
from pathlib import Path
from trial_policy import validate

def install(bundle,output):
    a=bundle['approval'];validate(a,a.get('run',''))
    output.mkdir(mode=0o700,exist_ok=False)
    (output/'approval.json').write_text(json.dumps(a));(output/'approval.json').chmod(0o600)
    config=[]
    for role,ip in (('main','173.212.219.169'),('database','79.143.176.173')):
        key=bundle[role+'_key'];known=bundle[role+'_known_hosts']
        if not key.startswith('-----BEGIN OPENSSH PRIVATE KEY-----') or len(key)>16384:raise ValueError('restricted OpenSSH key required')
        if not known.strip() or len(known)>32768:raise ValueError('pinned host key required')
        private=output/(role+'.key');private.write_text(key);private.chmod(0o600)
        hosts=output/(role+'.known_hosts');hosts.write_text(known);hosts.chmod(0o600)
        config.extend(['Host cohost-'+role,'  HostName '+ip,'  User root','  BatchMode yes','  IdentitiesOnly yes',
            '  StrictHostKeyChecking yes','  IdentityFile '+str(private.resolve()),'  UserKnownHostsFile '+str(hosts.resolve()),
            '  ControlMaster auto','  ControlPath '+str(output.resolve()/'%C'),'  ControlPersist 10','  ConnectTimeout 5','  ServerAliveInterval 5','  ServerAliveCountMax 1','  ExitOnForwardFailure yes'])
    (output/'ssh_config').write_text('\n'.join(config)+'\n');(output/'ssh_config').chmod(0o600)

if __name__=='__main__':install(json.loads(os.environ['COHOST_TRIAL_BUNDLE']),Path(sys.argv[1]))
