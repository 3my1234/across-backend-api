"""Real loopback SSH multiplexing during seed, with simulated host sensors.
Never connects to a VPS or changes the system SSH daemon/configuration.
"""
import json,os,socket,subprocess,tempfile,threading,time
from pathlib import Path
from trial_runner import Runner
from install_ci_bundle import control_path

class PeerReplay:
    def __init__(self,root,run,pg,seed,output):
        if os.environ.get('GITHUB_ACTIONS')!='true' or os.geteuid()!=0:raise ValueError('root disposable CI only')
        self.root=root;self.run=run;self.pg=pg;self.seed=seed;self.output=output
        self.stop=threading.Event();self.drop=threading.Event();self.events=[];self.threads=[];self.daemon=None;self.runner=None
    def __enter__(self):
        # sshd StrictModes rejects authorized_keys below world-writable/tmp.
        # A private CI-only directory under/root also fits the socket limit.
        self.temp=tempfile.TemporaryDirectory(prefix='axpeer-',dir='/root');self.base=Path(self.temp.name);self.base.chmod(0o700)
        try:
            self.setup()
            return self
        except BaseException:
            self.__exit__(None,None,None)
            if (self.output/'replay-sshd.log').exists():print((self.output/'replay-sshd.log').read_text()[-5000:],flush=True)
            raise
    def setup(self):
        def call(*args):return subprocess.check_output(args,text=True,timeout=15).strip()
        for key in ('host','main-key','database-key'):call('ssh-keygen','-q','-t','ed25519','-N','','-f',str(self.base/key))
        with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
        auth=[];config=[]
        for role,ident in (('main',self.seed),('database',self.pg)):
            base=self.base/role/'run';base.mkdir(parents=True,mode=0o700);base.chmod(0o700)
            (base/'manifest.json').write_text(json.dumps(dict(run=self.run,role=role,load_authorized=True,fixture_preparation_authorized=True,container_ids=[ident],services={('seed' if role=='main' else 'postgres'):ident})))
            # handle() binds manifest nonce to directory name.
            renamed=base.with_name(self.run);base.rename(renamed);base=renamed
            helper=self.root/'deploy/cohost-lab/access.py'
            auth.append(f'restrict,command="/usr/bin/python3 {helper} {base}" '+(self.base/(role+'-key.pub')).read_text().strip())
            config.extend(['Host cohost-'+role,' HostName 127.0.0.1',' Port '+str(port),' User root',' BatchMode yes',' IdentitiesOnly yes',' StrictHostKeyChecking yes',' UserKnownHostsFile '+str(self.base/'known_hosts'),' IdentityFile '+str(self.base/(role+'-key')),' ControlMaster auto',' ControlPersist 10',' ControlPath '+control_path(self.base),' ConnectTimeout 5'])
            thread=threading.Thread(target=self.observe,args=(role,base,ident),daemon=True);thread.start();self.threads.append(thread)
        (self.base/'authorized_keys').write_text('\n'.join(auth)+'\n');(self.base/'authorized_keys').chmod(0o600)
        (self.base/'known_hosts').write_text(f'[127.0.0.1]:{port} '+(self.base/'host.pub').read_text())
        (self.base/'ssh_config').write_text('\n'.join(config)+'\n')
        (self.base/'sshd_config').write_text(f'Port {port}\nListenAddress 127.0.0.1\nHostKey {self.base}/host\nPidFile {self.base}/sshd.pid\nAuthorizedKeysFile {self.base}/authorized_keys\nStrictModes yes\nUsePAM yes\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\nAllowTcpForwarding no\nLogLevel VERBOSE\n')
        Path('/run/sshd').mkdir(exist_ok=True)
        self.log=(self.output/'replay-sshd.log').open('w')
        self.daemon=subprocess.Popen(['/usr/sbin/sshd','-D','-e','-f',str(self.base/'sshd_config')],stdout=self.log,stderr=self.log)
        deadline=time.monotonic()+10
        while True:
            with socket.socket() as sock:
                if sock.connect_ex(('127.0.0.1',port))==0:break
            if self.daemon.poll() is not None or time.monotonic()>deadline:raise RuntimeError('private SSH daemon unavailable')
            time.sleep(.1)
        self.runner=Runner(self.root,self.base/'ssh_config',dict(run=self.run),self.output)
        for role in ('main','database'):self.runner.ssh(role,'status',timeout=15)
        self.relay=threading.Thread(target=self.runner.leases,daemon=True);self.relay.start()
        deadline=time.monotonic()+10
        while not self.runner.previous:
            self.runner.check()
            if time.monotonic()>deadline:raise RuntimeError('replay lease startup failed')
            time.sleep(.05)
    def observe(self,role,base,ident):
        start=time.monotonic();last=0
        while not self.stop.wait(.1):
            if self.drop.is_set() and role=='database':
                (base/'local.lease').unlink(missing_ok=True)
            elif time.monotonic()-last>=1:
                (base/'local.lease').touch();last=time.monotonic()
            peer=base/'peer.lease'
            if time.monotonic()-start>15 and (not peer.exists() or time.time()-peer.stat().st_mtime>5):
                self.events.append(dict(role=role,event='peer_expired',unix=time.time()))
                subprocess.run(['docker','stop','--timeout','2',ident],check=True,timeout=15)
                return
    def seed_database(self):
        self.runner.lab('seed','seed',self.output/'seed.log')
        self.runner.check()
        self.events.append(dict(event='seed_completed_with_live_peer_relay',unix=time.time()))
    def verify_fault_stop(self):
        # Exercise actual stale-status rejection and automatic test-only stop,
        # after successful seeding/count validation, not as a capacity result.
        self.drop.set();deadline=time.monotonic()+20
        while not self.runner.error:
            if time.monotonic()>deadline:raise RuntimeError('missing stale-peer rejection')
            time.sleep(.1)
        while not any(x.get('role')=='database' and x['event']=='peer_expired' for x in self.events):
            if time.monotonic()>deadline:raise RuntimeError('missing five-second peer stop')
            time.sleep(.1)
        self.events.append(dict(event='expected_stale_peer_fault_rejected',error_class=type(self.runner.error).__name__,unix=time.time()))
    def __exit__(self,*unused):
        self.stop.set()
        if self.runner:self.runner.stop.set()
        for thread in self.threads:thread.join(timeout=16)
        if hasattr(self,'relay'):self.relay.join(timeout=5)
        if hasattr(self,'base'):
            for role in ('main','database'):
                subprocess.run(['ssh','-F',str(self.base/'ssh_config'),'-O','exit','cohost-'+role],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=5,check=False)
        if self.daemon:
            self.daemon.terminate()
            try:self.daemon.wait(timeout=5)
            except subprocess.TimeoutExpired:self.daemon.kill();self.daemon.wait()
        if hasattr(self,'log'):self.log.close()
        (self.output/'peer-replay.json').write_text(json.dumps(dict(events=self.events,sensors='simulated healthy host observations',transport='real pinned loopback SSH',production_hosts_contacted=False),indent=2))
        if hasattr(self,'temp'):self.temp.cleanup()
