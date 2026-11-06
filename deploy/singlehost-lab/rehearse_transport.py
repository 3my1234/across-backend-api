"""Disposable real SSH, local guard fault and systemd hard-stop integration."""
import base64,json,os,shutil,socket,subprocess,sys,time,urllib.request
from pathlib import Path
from runtime import call,inventory,identity
from policy import STAGES

def exercise(root,base,output,sentinel):
    if os.environ.get('GITHUB_ACTIONS')!='true' or os.geteuid()!=0:raise ValueError('disposable CI only')
    run=base.name;source=call(['git','-c','safe.directory='+str(root),'rev-parse','HEAD']).decode().strip()
    import hashlib
    approval=dict(run=run,topology='singlehost-database-vps',host='79.143.176.173',authorized=True,approved_unix=int(time.time()),
        spending_cap_usd=0,aggregate_cpu=3,aggregate_memory=5905580032,application_cpu=1,application_memory=2684354560,
        database_cpu=2,database_memory=3221225472,generator_cpu=2,generator_memory=4294967296,lifetime_seconds=7200,
        health_scope='backend-api-blocking-homepage-advisory-v1',timing_policy='http-2s-observer-2.5s-v1',stages=[list(s) for s in STAGES],source_commit=source,
        compose_sha256=hashlib.sha256((base/'compose.json').read_bytes()).hexdigest(),image_archive_sha256='a'*64,verifier_sha256=hashlib.sha256((base/'verifier.bin').read_bytes()).hexdigest(),snapshot_sha256=json.loads((output/'fixture.json').read_text())['sha256'] if (output/'fixture.json').exists() else 'f'*64)
    approval.update(json.loads((base/'api-artifact.json').read_text()))
    (base/'approval.json').write_text(json.dumps(approval));(base/'approval.json').chmod(0o600)
    (base/'supervisor.json').write_text(json.dumps(dict(run=run,production_container_ids=[sentinel],deadline_unix=time.time()+7200)))
    (base/'setup.ready').touch()
    # Simulated public sensors ONLY in this disposable replay; real container,
    # CPU/memory/disk observation, fault decision and owned-stop code execute.
    sys.path.insert(0,str(root/'deploy/cohost-lab'))
    from watchdog import BLOCKING_URLS,HEALTH_SCOPE
    baseline=dict(health_scope=HEALTH_SCOPE,timing_policy='http-2s-observer-2.5s-v1',observed_seconds=310,finished_unix=time.time(),
        urls={url:dict(successful_samples=60,p95_seconds=.2,observer_p95_seconds=.3) for url in BLOCKING_URLS})
    (base/'baseline.json').write_text(json.dumps(baseline))
    wrapper=base/'ci-monitor-replay.py'
    wrapper.write_text('import sys\nfrom pathlib import Path\nsys.path.insert(0,'+repr(str(root/'deploy/singlehost-lab'))+')\nimport monitor\nfrom watchdog import state,BLOCKING_URLS\nbase=Path('+repr(str(base))+')\ndef sensors(ids,details):\n return [(u,not (base/"inject-public-fault").exists(),.1) for u in BLOCKING_URLS],state(ids)\nmonitor.collect_checks=sensors\nsys.argv=["monitor",str(base)]\nmonitor.main()\n')
    guard=sshd=tunnel=runner=None;config=base/'ssh_config';result={}
    def ssh(command,input=None,timeout=30):return call(['ssh','-F',str(config),'trial',command],timeout,input=input,stderr=subprocess.PIPE)
    try:
        guard=subprocess.Popen(['python3',str(wrapper)],stdout=(output/'guard-replay.log').open('wb'),stderr=subprocess.STDOUT)
        deadline=time.monotonic()+15
        while not (base/'local.lease').exists():
            if guard.poll() is not None or time.monotonic()>deadline:raise RuntimeError('real local monitor did not start')
            time.sleep(.1)
        # Exercise the native archive loader while the NEW independent guard is
        # actually inspecting running Docker containers. No Python stdin pipe.
        m,rows=inventory(base)
        images=sorted({row['Image'] for row in rows})
        archive=base/'import-replay.tar'
        subprocess.run(['docker','save','--output',str(archive),*images],check=True,timeout=60)
        subprocess.run(['gzip','-1',str(archive)],check=True,timeout=60)
        from import_process import load_archive
        from supervise import guard_ready
        with (output/'native-import-replay.log').open('w') as log:load_archive(archive.with_suffix('.tar.gz'),log,lambda:guard_ready(base))
        result['native_import_with_running_local_guard_passed']=True
        for name in ('host','client'):subprocess.run(['ssh-keygen','-q','-t','ed25519','-N','','-f',str(base/name)],check=True)
        from install_host import key_line
        # Point forced-command at the reviewed source; the live package uses an
        # identical relative src/deploy tree installed under the protected root.
        (base/'src/deploy').mkdir(parents=True)
        for name in ('singlehost-lab','cohost-lab'):(base/'src/deploy'/name).symlink_to(root/'deploy'/name,target_is_directory=True)
        line=key_line(base,(base/'client.pub').read_text(),time.strftime('%Y%m%d%H%M%SZ',time.gmtime(time.time()+1800)))
        auth=base/'authorized_keys';auth.write_text(line+'\n');auth.chmod(0o600)
        with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
        (base/'sshd_config').write_text(f'Port {port}\nListenAddress 127.0.0.1\nHostKey {base}/host\nAuthorizedKeysFile {auth}\nPermitRootLogin prohibit-password\nPubkeyAuthentication yes\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nStrictModes yes\nAllowTcpForwarding local\nLogLevel VERBOSE\n')
        Path('/run/sshd').mkdir(exist_ok=True)
        sshd=subprocess.Popen(['/usr/sbin/sshd','-D','-e','-f',str(base/'sshd_config')],stdout=subprocess.DEVNULL,stderr=(output/'sshd-replay.log').open('wb'))
        known=base/'known_hosts';known.write_text(f'[127.0.0.1]:{port} '+(base/'host.pub').read_text())
        # Exercise the actual CI client's multiplexing/control configuration,
        # not a simpler hand-written SSH configuration which can hide failures.
        from install_ci import install
        credentials=base/'replay-credentials'
        install(dict(approval=approval,key=(base/'client').read_text(),known_hosts=known.read_text()),credentials)
        config=credentials/'ssh_config'
        text=config.read_text().replace('Host cohost-main cohost-database','Host trial cohost-main cohost-database').replace('  HostName 79.143.176.173',f'  HostName 127.0.0.1\n  Port {port}')
        config.write_text(text)
        time.sleep(.3)
        status=json.loads(ssh('status'))
        if status['run']!=run or status['topology']!='singlehost':raise ValueError('wrong restricted endpoint')
        # Use the actual coordinator's concurrent status polling and role aliases.
        from runner import SingleHostRunner
        runner=SingleHostRunner(root,config,approval,output)
        runner.start()
        api_identity=json.loads(ssh('api-identity'))
        if api_identity.get('both_running_apis_verified') is not True or api_identity.get('api_sha256')!=approval['api_sha256']:raise ValueError('SSH running-API verification failed')
        (output/'api-running.json').write_text(json.dumps(api_identity,indent=2))
        result['running_api_identity_over_restricted_ssh_passed']=True
        def control(op,input=None):return ssh('control '+base64.b64encode(json.dumps(op).encode()).decode(),input=input)
        value=control(dict(action='exec',service='postgres',args=['psql','-U','lab','-d','capacity_lab','-XAt','-v','ON_ERROR_STOP=1']),input=b'SELECT 1;')
        if value.strip()!=b'1':raise ValueError('synthetic PostgreSQL control failed')
        control(dict(action='exec',service='api1',args=['curl','-fsS','--max-time','3','http://gateway:8080/api/v1/ready']))
        denied=subprocess.run(['ssh','-F',str(config),'trial','control '+base64.b64encode(json.dumps(dict(action='exec',service='api1',args=['sh','-c','echo denied'])).encode()).decode()],capture_output=True,timeout=15)
        if denied.returncode==0:raise RuntimeError('unreviewed shell command allowed')
        blocked=subprocess.run(['ssh','-F',str(config),'-o','ControlMaster=no','-o','ControlPath=none','-o','ControlPersist=no','-W','127.0.0.1:5432','trial'],capture_output=True,timeout=15)
        if blocked.returncode==0 or b'administratively prohibited' not in blocked.stderr:raise RuntimeError('non-gateway forwarding not explicitly denied')
        with socket.socket() as sock:sock.bind(('127.0.0.1',8080));forward=8080
        from http_forward import start as start_forward
        tunnel=start_forward(config,'cohost-main',forward,output/'http-forward.log')
        deadline=time.monotonic()+15
        while True:
            try:
                with urllib.request.urlopen(f'http://127.0.0.1:{forward}/api/v1/ready',timeout=3) as response:
                    if json.load(response).get('ok') is not True:raise ValueError('forwarded API unhealthy')
                break
            except Exception:
                if tunnel.poll() is not None or time.monotonic()>deadline:raise
                time.sleep(.2)
        result['restricted_control_and_http_only_forward_passed']=True
        # Exercise the actual staged coordinator, not only the direct smoke
        # command. Keep its normal relative output argument to catch Docker
        # bind-mount handling; exact live workload and gates remain unchanged.
        staged=Path(os.path.relpath(root/'singlehost-coordinator-replay',Path.cwd()))
        staged.mkdir()
        runner.output=staged.resolve()
        # The preceding direct smoke is a separate run: old message timestamps
        # and journal inventory must not be attributed to this staged replay.
        runner.sql('UPDATE capacity_lab_session_run SET run_id=gen_random_uuid();')
        runner.service('notification-provider')
        runner.measure(STAGES[0])
        result['actual_staged_coordinator_smoke_passed']=True
        runner.check();runner.stop.set();runner.children_thread.join(timeout=12)
        (base/'inject-public-fault').touch()
        guard.wait(timeout=20)
        _,rows=inventory(base)
        if any(row['State']['Running'] for row in rows):raise RuntimeError('local guard fault did not stop owned services')
        if call(['docker','inspect','--format','{{.State.Running}}',sentinel]).decode().strip()!='true':raise RuntimeError('guard affected sentinel')
        result['local_guard_public_fault_stop_passed']=True
        # Exercise the actual ExecStopPost cleanup command under a short disposable
        # RuntimeMaxSec; live policy still requires7200seconds and is not changed.
        unit=identity(run)+'deadline-replay'
        helper=root/'deploy/singlehost-lab/supervise.py'
        subprocess.run(['systemd-run','--unit',unit,'--property','RuntimeMaxSec=2','--property','TimeoutStopSec=150',
            '--property',f'ExecStopPost=/usr/bin/python3 {helper} cleanup {base}','/bin/sleep','300'],check=True,timeout=15)
        deadline=time.monotonic()+40
        while not (base/'cleanup.finalized').exists():
            if time.monotonic()>deadline:raise RuntimeError('hard-stop ExecStopPost did not finalize cleanup')
            time.sleep(.2)
        if inventory(base)[0]['container_ids']:raise RuntimeError('hard-stop cleanup left test containers')
        if call(['docker','inspect','--format','{{.State.Running}}',sentinel]).decode().strip()!='true':raise RuntimeError('hard-stop affected sentinel')
        result['systemd_deadline_cleanup_passed']=True
        (output/'transport-and-guard.json').write_text(json.dumps(result,indent=2))
        return result
    finally:
        if runner is not None:runner.stop.set()
        for process in (tunnel,sshd,guard):
            if process is not None and process.poll() is None:
                process.terminate()
                try:process.wait(timeout=5)
                except subprocess.TimeoutExpired:process.kill();process.wait(timeout=5)
        # Never place bearer fixture directories under public artifact roots.
        staged=root/'singlehost-coordinator-replay'
        if staged.is_dir():shutil.copytree(staged,output/'coordinator-stage',dirs_exist_ok=True)
        private=root/('private-'+run)
        if private.is_dir():shutil.rmtree(private)
        for name in ('watchdog-error.json','sample.json','watchdog.jsonl'):
            path=base/name
            if path.is_file():
                shutil.copyfile(path,output/('coordinator-'+name))
                if name=='watchdog-error.json':print('Disposable guard diagnostic: '+path.read_text(),flush=True)
