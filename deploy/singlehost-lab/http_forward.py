"""Dedicated forwarding connection, independent of control-RPC SSH masters."""
import subprocess

def start(config,alias,port,log):
    if alias not in ('cohost-main','trial') or type(port)!=int or not 1<=port<=65535:raise ValueError('unreviewed HTTP forwarding endpoint')
    with log.open('wb') as errors:
        return subprocess.Popen(['ssh','-F',str(config),'-o','ControlMaster=no','-o','ControlPath=none','-o','ControlPersist=no','-N','-L',f'127.0.0.1:{port}:172.29.248.10:8080',alias],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=errors,start_new_session=True)
