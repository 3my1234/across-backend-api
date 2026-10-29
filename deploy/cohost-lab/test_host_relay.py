import json,socket,socketserver,subprocess,threading,unittest
from unittest.mock import patch
from host_relay import server,target

class RelayTests(unittest.TestCase):
    def fixture(self):
        run='cohost-'+'a'*16;ident='b'*64;network='c'*64
        manifest=dict(run=run,role='database',load_authorized=False,services=dict(postgres=ident),networks=[network])
        rows=[dict(Id=ident,Config=dict(Labels={'atlxpres.cohost.run':run}),State=dict(Running=True),NetworkSettings=dict(Networks=dict(test=dict(NetworkID=network,IPAddress='172.31.17.2'))))]
        networks=[dict(Internal=True,Labels={'atlxpres.cohost.run':run},IPAM=dict(Config=[dict(Subnet='172.31.17.0/24')]))]
        return manifest,rows,networks
    def test_exact_owned_internal_target_and_fixed_loopback_port(self):
        m,rows,networks=self.fixture()
        with patch('host_relay.subprocess.check_output',side_effect=[json.dumps(rows),json.dumps(networks)]):
            self.assertEqual(target(m),(('127.0.0.1',55432),('172.31.17.2',5432)))
    def test_wrong_owner_or_external_network_refused(self):
        for case in ('owner','external','address'):
            m,rows,networks=self.fixture()
            if case=='owner':rows[0]['Config']['Labels']['atlxpres.cohost.run']='unrelated'
            if case=='external':networks[0]['Internal']=False
            if case=='address':rows[0]['NetworkSettings']['Networks']['test']['IPAddress']='172.30.1.2'
            with self.subTest(case=case),patch('host_relay.subprocess.check_output',side_effect=[json.dumps(rows),json.dumps(networks)]),self.assertRaises(ValueError):target(m)
    def test_gateway_relay_denied_without_load_approval(self):
        m,_,_=self.fixture();m['role']='main'
        with self.assertRaisesRegex(ValueError,'load approval'):target(m)
    def test_real_tcp_bytes_in_both_directions_and_no_public_bind(self):
        with self.assertRaises(ValueError):server(('0.0.0.0',0),('127.0.0.1',1))
        class Echo(socketserver.BaseRequestHandler):
            def handle(self):self.request.sendall(b'reply:'+self.request.recv(100))
        with socketserver.TCPServer(('127.0.0.1',0),Echo) as upstream:
            threading.Thread(target=upstream.serve_forever,daemon=True).start()
            with server(('127.0.0.1',0),upstream.server_address) as relay:
                threading.Thread(target=relay.serve_forever,daemon=True).start()
                with socket.create_connection(relay.server_address,timeout=2) as connection:
                    connection.sendall(b'protocol-test');self.assertEqual(connection.recv(100),b'reply:protocol-test')
                relay.shutdown()
            upstream.shutdown()

if __name__=='__main__':unittest.main()
