"""Install operator-supplied temporary restricted keys on the free runner only."""
import json,os,re,sys,tempfile,shutil,subprocess
from pathlib import Path
from trial_policy import validate

def control_path(directory):
    # %C expands to40 hex characters. OpenSSH may append a dot+16-character
    # temporary suffix during atomic socket creation. Leave room for both.
    path=str(directory/'%C')
    if len(path.replace('%C','0'*40).encode())+17>103:
        raise ValueError('SSH control socket including temporary suffix is too long')
    return path

def socket_directory(output):
    directory=Path(tempfile.mkdtemp(prefix='axct-',dir='/tmp'))
    directory.chmod(0o700)
    try:control_path(directory)
    except BaseException:directory.rmdir();raise
    st=directory.stat()
    (output/'socket-directory.json').write_text(json.dumps(dict(path=str(directory),uid=st.st_uid,dev=st.st_dev,ino=st.st_ino)))
    return directory

def cleanup(output):
    record=output/'socket-directory.json'
    if not record.exists():return
    data=json.loads(record.read_text());directory=Path(data['path'])
    if directory.parent!=Path('/tmp') or not directory.name.startswith('axct-') or directory.is_symlink():raise ValueError('unowned socket directory')
    if not directory.exists():return
    st=directory.stat()
    if (st.st_uid,st.st_dev,st.st_ino)!=(data['uid'],data['dev'],data['ino']) or st.st_uid!=os.getuid() or st.st_mode&0o077:
        raise ValueError('socket directory ownership changed')
    for role in ('main','database'):
        subprocess.run(['ssh','-F',str(output/'ssh_config'),'-O','exit','cohost-'+role],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=5,check=False)
    shutil.rmtree(directory)


def install(bundle,output):
    a=bundle['approval'];validate(a,a.get('run',''))
    output.mkdir(mode=0o700,exist_ok=False)
    (output/'approval.json').write_text(json.dumps(a));(output/'approval.json').chmod(0o600)
    sockets=socket_directory(output)
    config=[]
    for role,ip in (('main','173.212.219.169'),('database','79.143.176.173')):
        key=bundle[role+'_key'];known=bundle[role+'_known_hosts']
        if not key.startswith('-----BEGIN OPENSSH PRIVATE KEY-----') or len(key)>16384:raise ValueError('restricted OpenSSH key required')
        if not known.strip() or len(known)>32768:raise ValueError('pinned host key required')
        private=output/(role+'.key');private.write_text(key);private.chmod(0o600)
        hosts=output/(role+'.known_hosts');hosts.write_text(known);hosts.chmod(0o600)
        config.extend(['Host cohost-'+role,'  HostName '+ip,'  User root','  BatchMode yes','  IdentitiesOnly yes',
            '  StrictHostKeyChecking yes','  IdentityFile '+str(private.resolve()),'  UserKnownHostsFile '+str(hosts.resolve()),
            '  ControlMaster auto','  ControlPath '+control_path(sockets),'  ControlPersist 10','  ConnectTimeout 5','  ServerAliveInterval 5','  ServerAliveCountMax 1','  ExitOnForwardFailure yes'])
    (output/'ssh_config').write_text('\n'.join(config)+'\n');(output/'ssh_config').chmod(0o600)

if __name__=='__main__':
    if sys.argv[1]=='--cleanup':cleanup(Path(sys.argv[2]))
    else:install(json.loads(os.environ['COHOST_TRIAL_BUNDLE']),Path(sys.argv[1]))
