"""Install/remove exact reviewed expiring test keys, only after setup approval.
Does not edit sshd settings. Default prints proposed public authorized_keys lines.
"""
import argparse,json,os,re,time
from pathlib import Path
import subprocess


def lines(base):
    request=json.loads((base/'request.json').read_text());run=request['run'];role=request['role']
    if not re.fullmatch(r'cohost-[a-f0-9]{16}',run):raise ValueError('invalid nonce')
    deadline=request.get('key_expiry_utc','')
    if not re.fullmatch(r'[0-9]{14}Z',deadline):raise ValueError('fixed key expiry required')
    helper=f'/usr/bin/python3 /root/atlxpres-cohost/{run}/tools/access.py /root/atlxpres-cohost/{run}'
    result=[]
    for kind in (['ci-main'] if role=='main' else ['ci-database','main-database']):
        key=(base/(kind+'.pub')).read_text().strip().split()
        if len(key)<2 or key[0]!='ssh-ed25519' or not re.fullmatch(r'[A-Za-z0-9+/=]+',key[1]):raise ValueError('invalid public key')
        scope=(f'port-forwarding,permitopen="127.0.0.1:{58080 if role=="main" else 55432}",' if kind!='ci-database' else '')
        command='/bin/false' if kind=='main-database' else helper
        result.append(f'restrict,{scope}expiry-time="{deadline}",command="{command}" '+key[0]+' '+key[1]+' '+run+'-'+kind)
    return result


def main():
    ap=argparse.ArgumentParser();ap.add_argument('directory',type=Path);ap.add_argument('--execute',action='store_true');ap.add_argument('--remove',action='store_true');a=ap.parse_args();base=a.directory.resolve()
    proposed=lines(base)
    if not a.execute:print('\n'.join(proposed));return
    if os.name!='posix' or os.geteuid()!=0:raise ValueError('Linux root required')
    approval=json.loads((base/'approval.json').read_text())
    request=json.loads((base/'request.json').read_text())
    if approval.get('authorized_setup') is not True or approval.get('run')!=request['run'] or approval.get('load_authorized') is not False or (not a.remove and not 0<=time.time()-approval.get('approved_unix',0)<=86400):raise ValueError('matching current setup approval required')
    if base!=Path('/root/atlxpres-cohost')/request['run']:raise ValueError('fixed root run directory required')
    policy=subprocess.check_output(['sshd','-T','-C','user=root,host=localhost,addr=127.0.0.1'],text=True,timeout=10)
    if not any(x.split()[0]=='authorizedkeysfile' and x.split()[1]=='.ssh/authorized_keys' for x in policy.splitlines()):raise ValueError('nondefault key path; do not alter sshd policy')
    path=Path('/root/.ssh/authorized_keys')
    if path.is_symlink() or not path.is_file():raise ValueError('existing regular key file required')
    info=path.stat()
    if info.st_uid!=0 or info.st_mode&0o022:raise ValueError('unsafe key file permissions')
    current=path.read_text();old=current.splitlines()
    if a.remove:
        updated='\n'.join(x for x in old if x not in proposed)+'\n'
    else:
        if any(request['run'] in x for x in old):raise ValueError('run keys already present')
        updated=current.rstrip('\n')+'\n'+'\n'.join(proposed)+'\n'
    temporary=path.with_name('authorized_keys.'+request['run'])
    fd=os.open(temporary,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(fd,'w') as out:out.write(updated);out.flush();os.fsync(out.fileno())
    if path.read_text()!=current:
        temporary.unlink()
        raise ValueError('key file changed concurrently; refuse overwrite')
    os.replace(temporary,path)
    print('Removed exact test key lines.' if a.remove else 'Installed expiring restricted test keys; sshd settings unchanged.')

if __name__=='__main__':main()
