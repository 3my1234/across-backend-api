"""Cheap disposable proof of private bridge reachability; no seed or live hosts."""
import json,os,socket,subprocess,time,uuid
def call(args):return subprocess.check_output(args,text=True,timeout=30).strip()
def main():
    if os.environ.get('GITHUB_ACTIONS')!='true':raise ValueError('disposable CI only')
    run='singlehost-network-'+uuid.uuid4().hex[:12];network=container=None
    try:
        network=call(['docker','network','create','--internal','--subnet','172.29.248.0/24','--ip-range','172.29.248.128/25','--gateway','172.29.248.1','--label','atlxpres.network-replay='+run,run])
        container=call(['docker','run','-d','--network',network,'--ip','172.29.248.10','--label','atlxpres.network-replay='+run,'--cpus','.25','--memory','64m','redis:7.2-alpine','redis-server','--port','8080','--save','','--appendonly','no'])
        deadline=time.monotonic()+10
        while True:
            try:
                with socket.create_connection(('172.29.248.10',8080),timeout=2) as connection:
                    host_source=connection.getsockname()[0]
                    connection.sendall(b'*1\r\n$4\r\nPING\r\n')
                    if connection.recv(64)!=b'+PONG\r\n':raise ValueError('private TCP response mismatch')
                break
            except Exception:
                if time.monotonic()>deadline:raise
                time.sleep(.1)
        state=json.loads(call(['docker','inspect',container]))[0]
        if state['HostConfig']['PortBindings']:raise ValueError('unexpected published port')
        network_state=json.loads(call(['docker','network','inspect',network]))[0]
        if host_source!='172.29.248.1' or network_state['IPAM']['Config'][0]['Gateway']!=host_source:raise ValueError('synthetic trusted edge identity mismatch')
        print(json.dumps(dict(private_host_gateway_reachability=True,host_source=host_source,ipam=network_state['IPAM'],published_ports=False,seed_started=False,live_hosts_contacted=False)))
    finally:
        if container:
            if call(['docker','inspect','--format','{{index .Config.Labels "atlxpres.network-replay"}}',container])!=run:raise ValueError('container ownership mismatch')
            subprocess.run(['docker','rm','-f',container],check=True,timeout=30)
        if network:subprocess.run(['docker','network','rm',network],check=True,timeout=30)
if __name__=='__main__':main()
