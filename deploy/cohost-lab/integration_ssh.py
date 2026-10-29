"""Disposable CI SSH/PG protocol proof. NEVER connects to either existing VPS."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time


def call(*args,**kw):return subprocess.check_output(args,text=True,timeout=20,**kw).strip()

def free_port():
    with socket.socket() as s:s.bind(("127.0.0.1",0));return s.getsockname()[1]

def wait_port(port):
    for _ in range(100):
        with socket.socket() as s:
            if s.connect_ex(("127.0.0.1",port))==0:return
        time.sleep(.1)
    raise AssertionError("listener did not start")


def main():
    assert os.environ.get("GITHUB_ACTIONS")=="true" and os.geteuid()==0,"CI root only"
    procs=[];pg=None
    with tempfile.TemporaryDirectory() as directory:
        base=Path(directory);base.chmod(0o700)
        ssh_port=free_port();db_port=free_port()
        try:
            pg=call("docker","run","-d","--label","atlxpres.cohost.run=cohost-"+"d"*16,
                "-p",f"127.0.0.1:{db_port}:5432","-e","POSTGRES_PASSWORD=lab-disposable-only",
                "-e","POSTGRES_USER=lab","-e","POSTGRES_DB=capacity_lab","postgres:16-alpine")
            for _ in range(100):
                result=subprocess.run(["docker","exec",pg,"pg_isready","-U","lab","-d","capacity_lab"],capture_output=True)
                if result.returncode==0:break
                time.sleep(.2)
            else:raise AssertionError("test PG not ready")
            call("docker","exec",pg,"psql","-U","lab","-d","capacity_lab","-c",
                "CREATE TABLE capacity_lab_identity(marker text); INSERT INTO capacity_lab_identity VALUES ('disposable-compose-only');")
            for key in ("host","client"):
                call("ssh-keygen","-q","-t","ed25519","-N","","-f",str(base/key))
            public=(base/"client.pub").read_text().strip()
            auth=base/"authorized_keys"
            auth.write_text(f'restrict,port-forwarding,permitopen="127.0.0.1:{db_port}",command="/bin/false" '+public+"\n")
            auth.chmod(0o600)
            config=base/"sshd_config"
            config.write_text(f"Port {ssh_port}\nListenAddress 127.0.0.1\nHostKey {base/'host'}\nPidFile {base/'sshd.pid'}\nAuthorizedKeysFile {auth}\nStrictModes yes\nUsePAM no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\nAllowTcpForwarding local\nAllowStreamLocalForwarding local\nGatewayPorts no\nLogLevel ERROR\n")
            Path("/run/sshd").mkdir(exist_ok=True)
            log=(base/"sshd.log").open("w")
            daemon=subprocess.Popen(["/usr/sbin/sshd","-D","-e","-f",str(config)],stdout=log,stderr=log);procs.append(daemon)
            wait_port(ssh_port)
            common=["ssh","-i",str(base/"client"),"-p",str(ssh_port),"-o","BatchMode=yes",
                "-o","StrictHostKeyChecking=yes","-o","UserKnownHostsFile="+str(base/"known_hosts"),
                "-o","ExitOnForwardFailure=yes","-o","ConnectTimeout=3","-o","ServerAliveInterval=1","-o","ServerAliveCountMax=2"]
            known=f"[127.0.0.1]:{ssh_port} "+(base/"host.pub").read_text().strip()+"\n"
            (base/"known_hosts").write_text(known)
            sock=base/".s.PGSQL.55432"
            forward=subprocess.Popen(common+["-N","-L",f"{sock}:127.0.0.1:{db_port}","root@127.0.0.1"],stdout=subprocess.DEVNULL,stderr=log);procs.append(forward)
            for _ in range(50):
                if sock.exists():break
                if forward.poll() is not None:raise AssertionError("SSH failed: "+(base/"sshd.log").read_text())
                time.sleep(.1)
            env={**os.environ,"PGPASSWORD":"lab-disposable-only"}
            destination=call("psql","-h",str(base),"-p","55432","-U","lab","-d","capacity_lab","-XAtc",
                "SELECT current_database() || ':' || marker FROM capacity_lab_identity",env=env)
            assert destination=="capacity_lab:disposable-compose-only",destination
            # Forced command denies arbitrary shell, even for the accepted key.
            assert subprocess.run(common+["root@127.0.0.1","echo should-not-run"],stdout=subprocess.PIPE,stderr=log).returncode!=0
            bad_sock=base/"denied.sock"
            denied=subprocess.Popen(common+["-N","-L",f"{bad_sock}:127.0.0.1:5432","root@127.0.0.1"],stdout=subprocess.DEVNULL,stderr=log);procs.append(denied)
            for _ in range(50):
                if bad_sock.exists():break
                time.sleep(.1)
            with socket.socket(socket.AF_UNIX) as s:
                s.settimeout(3);s.connect(str(bad_sock));s.sendall(b"forbidden")
                try: response=s.recv(32)
                except ConnectionResetError:response=b""
                assert response==b"","forbidden destination accepted"
            print(json.dumps(dict(ssh_unix_pg_protocol="passed",saved_marker=destination,
                arbitrary_command="denied",non_test_port="denied",public_database_port=False)))
        finally:
            for process in reversed(procs):
                process.terminate()
                try:process.wait(timeout=5)
                except subprocess.TimeoutExpired:process.kill();process.wait()
            if pg:subprocess.run(["docker","rm","-fv",pg],check=True,stdout=subprocess.DEVNULL)

if __name__=="__main__":main()
