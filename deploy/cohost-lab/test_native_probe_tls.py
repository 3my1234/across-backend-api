"""Disposable local TLS server: never contacts production or generates user load."""
import ctypes as C,ctypes.util,http.server,os,pathlib,shutil,ssl,subprocess,tempfile,threading,time,unittest
import native_probe as n

@unittest.skipUnless(os.name=='posix' and ctypes.util.find_library('curl') and shutil.which('openssl'),'requires disposable Linux libcurl/OpenSSL')
class RealTLS(unittest.TestCase):
    def test_untrusted_certificate_rejected_verified_reuse_and_timeout(self):
        class Handler(http.server.BaseHTTPRequestHandler):
            protocol_version='HTTP/1.1'
            def log_message(self,*_):pass
            def do_GET(self):
                if self.path=='/slow':time.sleep(2.2)
                body=b'{"ok":false}' if self.path=='/not-ready' else b'{"ok":true}'
                self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers()
                try:self.wfile.write(body)
                except OSError:pass
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory);certificate=root/'cert.pem';key=root/'key.pem'
            subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=localhost','-addext','subjectAltName=DNS:localhost','-keyout',str(key),'-out',str(certificate)],check=True,capture_output=True,timeout=15)
            server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
            context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);context.load_cert_chain(certificate,key)
            server.socket=context.wrap_socket(server.socket,server_side=True)
            thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
            engine=n.Curl(n.URLS[0]);keep=[]
            def text(option,value):
                data=C.c_char_p(value.encode());keep.append(data);engine.set(option,data)
            origin='https://localhost:'+str(server.server_port)
            try:
                text(10002,origin+'/api/v1/ready')
                rejected=engine.request();self.assertFalse(rejected['transport_healthy']);self.assertNotEqual(rejected['exit_code'],0)
                # Trust only this disposable fixture CA; verification remains ON.
                text(10065,str(certificate))
                first=engine.request();second=engine.request()
                self.assertTrue(first['transport_healthy']);self.assertTrue(second['transport_healthy'])
                self.assertEqual(second['new_connections'],0)
                text(10002,origin+'/not-ready');self.assertFalse(engine.request()['transport_healthy'])
                text(10002,origin+'/slow');slow=engine.request()
                self.assertEqual(slow['exit_code'],28);self.assertFalse(slow['transport_healthy'])
                self.assertLess(slow['http_seconds'],2.15)
            finally:engine.close();server.shutdown();server.server_close();thread.join(timeout=3)

if __name__=='__main__':unittest.main()
