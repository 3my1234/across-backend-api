"""Loopback-only raw TCP relay to an exact owned internal-network test container."""
import argparse,ipaddress,json,os,selectors,socket,socketserver,subprocess
from pathlib import Path
from safety import validate_inventory

def target(manifest):
    role=manifest.get('role')
    if role not in ('main','database'):raise ValueError('invalid relay role')
    if role=='main' and manifest.get('load_authorized') is not True:raise ValueError('gateway relay requires load approval')
    service='postgres' if role=='database' else 'gateway'
    ident=manifest['services'][service]
    rows=json.loads(subprocess.check_output(['docker','inspect',ident],text=True,timeout=10))
    validate_inventory(dict(run=manifest['run'],container_ids=[ident]),rows)
    if not rows[0]['State']['Running']:raise ValueError('relay target not running')
    endpoints=rows[0].get('NetworkSettings',{}).get('Networks',{}) or {}
    found=[]
    for endpoint in endpoints.values():
        if endpoint.get('NetworkID') not in manifest.get('networks',[]):continue
        networks=json.loads(subprocess.check_output(['docker','network','inspect',endpoint['NetworkID']],text=True,timeout=10))
        if len(networks)!=1 or (networks[0].get('Labels') or {}).get('atlxpres.cohost.run')!=manifest['run'] or networks[0].get('Internal') is not True:
            raise ValueError('relay requires owned internal network')
        address=ipaddress.ip_address(endpoint['IPAddress'])
        if address.version!=4 or not address.is_private:raise ValueError('invalid test network address')
        configs=(networks[0].get('IPAM') or {}).get('Config') or []
        if not any(address in ipaddress.ip_network(c['Subnet']) for c in configs if c.get('Subnet') and ':' not in c['Subnet']):raise ValueError('target outside owned subnet')
        found.append(str(address))
    if len(found)!=1:raise ValueError('ambiguous owned relay target')
    return ('127.0.0.1',55432 if role=='database' else 58080),(found[0],5432 if role=='database' else 8080)

def server(listen,destination):
    if listen[0]!='127.0.0.1':raise ValueError('relay must be loopback only')
    class Handler(socketserver.BaseRequestHandler):
        def handle(self):
            try:
                with socket.create_connection(destination,timeout=3) as upstream,selectors.DefaultSelector() as selector:
                    upstream.settimeout(None)
                    selector.register(self.request,selectors.EVENT_READ,upstream)
                    selector.register(upstream,selectors.EVENT_READ,self.request)
                    while True:
                        for key,_ in selector.select(timeout=5):
                            data=key.fileobj.recv(65536)
                            if not data:return
                            key.data.sendall(data)
            except OSError:return
    class Relay(socketserver.ThreadingMixIn,socketserver.TCPServer):
        daemon_threads=True
        request_queue_size=64
        allow_reuse_address=False
    return Relay(listen,Handler)

def main():
    p=argparse.ArgumentParser();p.add_argument('manifest',type=Path);a=p.parse_args()
    info=a.manifest.stat()
    if os.geteuid()!=0 or info.st_uid!=0 or info.st_mode&0o022:raise ValueError('protected root manifest required')
    listen,destination=target(json.loads(a.manifest.read_text()))
    print(json.dumps(dict(relay='loopback-only',listen_port=listen[1],target_port=destination[1])),flush=True)
    with server(listen,destination) as relay:relay.serve_forever()

if __name__=='__main__':main()
