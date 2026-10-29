"""Read-only missing cohost prerequisites; never reads env, keys or customer data."""
import argparse,json,subprocess,socket
from pathlib import Path

def call(args):
    p=subprocess.run(args,capture_output=True,text=True,timeout=20)
    return dict(exit_code=p.returncode,output=p.stdout.strip() if p.returncode==0 else "unavailable")

ap=argparse.ArgumentParser();ap.add_argument('--role',choices=['main','database'],required=True);a=ap.parse_args()
r=dict(role=a.role,services_changed=False,settings_changed=False,database_queries=False)
r['docker']=call(['docker','info','--format','{{json .CgroupDriver}} {{json .CgroupVersion}}'])
r['controllers']=Path('/sys/fs/cgroup/cgroup.controllers').read_text().strip() if Path('/sys/fs/cgroup/cgroup.controllers').exists() else 'cgroup-v2-unavailable'
r['test_network_collision']=call(['docker','network','ls','--format','{{.ID}}'])
ids=r['test_network_collision']['output'].split() if r['test_network_collision']['exit_code']==0 else []
r['subnets']=call(['docker','network','inspect','--format','{{range .IPAM.Config}}{{.Subnet}} {{end}}',*ids]) if ids else dict(exit_code=1,output='unavailable')
r['loopback_ports']={}
for port in ([58080] if a.role=='main' else [55432]):
    s=socket.socket();s.settimeout(1)
    try: r['loopback_ports'][str(port)]='occupied' if s.connect_ex(('127.0.0.1',port))==0 else 'no-listener-observed'
    finally:s.close()
r['ssh_forward_policy']=call(['sshd','-T','-C','user=root,host=localhost,addr=127.0.0.1'])
if r['ssh_forward_policy']['exit_code']==0:
    allowed=('allowtcpforwarding ','allowstreamlocalforwarding ','disableforwarding ','permitopen ','gatewayports ','pubkeyauthentication ','permitrootlogin ')
    r['ssh_forward_policy']['output']='\n'.join(x for x in r['ssh_forward_policy']['output'].splitlines() if x.startswith(allowed))
r['limitations']='No connectivity proof, active bandwidth test, setup, SQL or load. Port check observes listener only; reservations must be checked immediately before setup.'
print(json.dumps(r,indent=2))
