"""MAIN-host exact-source migration runner. Preview is default; no live API/worker.

Requires separately approved production execution, a private candidate env file,
verified public CI toolkit and retained local API runtime image. Never downloads
credentials, images, database archives or packages. Never reroutes/restarts apps.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import stat
import subprocess
import uuid
from urllib.parse import parse_qs, urlsplit


class Stop(ValueError):
    """Only fixed, non-secret validation messages may reach operator output."""


def validate_target(values):
    if values.get('APP_ENV') != 'production' or values.get('DB_VERIFIED_VPN_HOST') != '10.77.0.2':
        raise Stop('Explicit production environment and verified VPN peer required')
    u = urlsplit(values.get('MIGRATION_DATABASE_URL', ''))
    query = parse_qs(u.query, keep_blank_values=True)
    if u.scheme not in ('postgres', 'postgresql') or u.hostname != '10.77.0.2' or (u.port or 5432) != 5432 or u.path != '/across_db' or any(k in query for k in ('host','hostaddr','port','service','servicefile')):
        raise Stop('Migration must target only the verified across_db peer')


def inventory(directory, revision):
    if directory.is_symlink():
        raise Stop('Toolkit cannot be a symlink')
    manifest_path = directory/'manifest.json'
    if manifest_path.is_symlink() or manifest_path.stat().st_size > 262144:
        raise Stop('Invalid toolkit manifest')
    manifest = json.loads(manifest_path.read_text())
    if not isinstance(manifest, dict) or (directory/'migrations').is_symlink():
        raise Stop('Invalid toolkit directory or manifest')
    if not re.fullmatch('[a-f0-9]{40}', revision) or manifest.get('revision') != revision or manifest.get('platform') != 'linux/amd64':
        raise Stop('Toolkit identity does not match approved revision/platform')
    entries = manifest.get('files', {})
    if not isinstance(entries, dict) or not 3 <= len(entries) <= 1000:
        raise Stop('Invalid toolkit inventory')
    for name, expected in entries.items():
        if not isinstance(name, str) or name not in ('across-config-check','across-migrate','run-approved-migration.py') and not re.fullmatch(r'migrations/[a-zA-Z0-9_-]+\.sql', name):
            raise Stop('Unexpected toolkit file')
        path = directory/name
        if path.is_symlink() or not path.is_file() or path.stat().st_size > 64*1024*1024 or not re.fullmatch('[a-f0-9]{64}', str(expected)):
            raise Stop('Invalid toolkit file metadata')
        if hashlib.sha256(path.read_bytes()).hexdigest() != expected:
            raise Stop('Toolkit checksum mismatch')
    if not all(key in entries for key in ('across-config-check','across-migrate')) or manifest.get('migration_count') != sum(name.startswith('migrations/') for name in entries):
        raise Stop('Toolkit executables or migration count mismatch')
    # The migrator reads this directory. No unlisted SQL may enter execution.
    actual = {p.relative_to(directory).as_posix() for p in (directory/'migrations').iterdir()}
    if actual != {name for name in entries if name.startswith('migrations/')} or (directory/'migrations').is_symlink():
        raise Stop('Migration directory contains unverified content')
    return manifest


def private_env(path):
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 262144:
        raise Stop('Expected private regular env file')
    if os.name == 'posix' and (path.stat().st_uid != 0 or stat.S_IMODE(path.stat().st_mode) & 0o077):
        raise Stop('Env file must be root-owned and mode0600')
    if os.name == 'posix':
        for parent in path.absolute().parents:
            if parent.is_symlink() or parent.stat().st_uid != 0 or stat.S_IMODE(parent.stat().st_mode) & 0o022:
                raise Stop('Private env directory must be root-owned and protected')
    values = {}
    for line in path.read_text().splitlines():
        if not line or line.startswith('#'):
            continue
        key, separator, value = line.partition('=')
        if not separator or not re.fullmatch('[A-Z][A-Z0-9_]*',key) or key in values:
            raise Stop('Env file must use unique literal KEY=value lines')
        values[key] = value
    validate_target(values)
    return values


def command(image, toolkit, name, owner, env_file=None, network='none', migrate=False):
    args = ['docker','run','--rm','--pull=never','--name',name,'--label','atl.release.owner='+owner,
            '--network',network,'--read-only','--no-healthcheck','--cap-drop','ALL',
            '--security-opt','no-new-privileges','--cpus','0.5','--memory','256m',
            '--pids-limit','64','--user','10001:10001','--workdir','/app']
    if env_file:
        args += ['--env-file',str(env_file)]
    binary = 'across-migrate' if migrate else 'across-config-check'
    args += ['--mount','type=bind,src='+str(toolkit/binary)+',dst=/candidate/'+binary+',readonly']
    if migrate:
        args += ['--mount','type=bind,src='+str(toolkit/'migrations')+',dst=/app/migrations,readonly']
    return args+['--entrypoint','/candidate/'+binary,image]


def logged_migration(log_path, invoke):
    # Create the exclusive private log BEFORE starting a mutating process.
    # pathlib.Path.open has no opener parameter; built-in open supports it.
    with open(log_path,'x',encoding='utf-8',opener=lambda path,flags:os.open(path,flags,0o600)) as log:
        result = invoke()
        log.write(result.stdout+'\n'+result.stderr)
        log.flush()
        return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--toolkit',type=Path,required=True)
    parser.add_argument('--revision',required=True)
    parser.add_argument('--runtime-image',required=True)
    parser.add_argument('--env-file',type=Path,required=True)
    parser.add_argument('--execute',action='store_true')
    parser.add_argument('--approval-ref',default='')
    args = parser.parse_args()
    if any(any(character in str(path) for character in ',\r\n') for path in (args.toolkit,args.env_file)):
        raise Stop('Unsupported mount path characters')
    manifest = inventory(args.toolkit,args.revision)
    private_env(args.env_file)
    if not re.fullmatch(r'sha256:[a-f0-9]{64}',args.runtime_image):
        raise Stop('Retained local runtime image ID required; no tag or pull')
    plan = {'revision':args.revision,'expected_migrations':manifest['migration_count'],'database':'across_db','verified_peer':'10.77.0.2',
            'runtime_image':args.runtime_image,'cpu_limit':0.5,'memory_limit':'256m','db_max_connections':2,
            'lock_timeout_seconds':3,'statement_timeout_seconds':120,'overall_migrator_seconds':180,
            'scope':'one owned migrator; no API/worker, restart, routing, image pull or database restore'}
    if not args.execute:
        print(json.dumps(dict(plan,preview_only=True,production_approval_required=True)))
        return
    if platform.system() != 'Linux' or platform.machine() not in ('x86_64','amd64') or os.geteuid() != 0 or not args.approval_ref.strip():
        raise Stop('Explicit approved execution requires root on MAIN Linux/amd64')
    route = subprocess.run(['ip','route','get','10.77.0.2'],capture_output=True,text=True,timeout=5)
    if route.returncode or 'dev wg-atl ' not in route.stdout or 'src 10.77.0.1 ' not in route.stdout:
        raise Stop('Verified WireGuard route does not match; no migration started')
    import fcntl
    for parent in [args.toolkit.absolute(), *args.toolkit.absolute().parents]:
        if parent.is_symlink() or parent.stat().st_uid != 0 or stat.S_IMODE(parent.stat().st_mode) & 0o022:
            raise Stop('Toolkit directory must be root-owned and protected')
    links = subprocess.run(['ip','-d','link','show','type','wireguard'],capture_output=True,text=True,timeout=5)
    if links.returncode or 'wg-atl:' not in links.stdout:
        raise Stop('Verified route interface is not WireGuard')
    lock_directory = Path('/run/atl-schema-release')
    if lock_directory.is_symlink(): raise Stop('Unexpected lock directory symlink')
    lock_directory.mkdir(mode=0o700,exist_ok=True)
    if lock_directory.stat().st_uid != 0 or stat.S_IMODE(lock_directory.stat().st_mode) & 0o077:
        raise Stop('Migration lock directory must be root-owned and private')
    lock_path = lock_directory/'operation.lock'
    if lock_path.is_symlink(): raise Stop('Unexpected lock symlink')
    with lock_path.open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX | fcntl.LOCK_NB)
        owner = uuid.uuid4().hex
        prefix = 'atl-schema-'+owner[:12]
        names = []
        for binary in ('across-config-check','across-migrate'):
            (args.toolkit/binary).chmod(0o755)
        (args.toolkit/'migrations').chmod(0o755)
        for source in (args.toolkit/'migrations').iterdir():
            source.chmod(0o644)
        toolkit, env_file = args.toolkit.resolve(), args.env_file.resolve()

        def run(label, tail, *, network='none', with_env=False, migrate=False, timeout=30):
            name = prefix+'-'+label
            names.append(name)
            return subprocess.run(command(args.runtime_image,toolkit,name,owner,env_file if with_env else None,network,migrate)+tail,capture_output=True,text=True,timeout=timeout)

        try:
            identity = run('identity',['--build-info'])
            build = json.loads(identity.stdout) if identity.returncode == 0 else {}
            if not isinstance(build,dict): raise Stop('Invalid binary identity')
            if build.get('revision') != args.revision or build.get('modified') is not False:
                raise Stop('Binary build identity mismatch; no migration started')
            preflight = run('preflight',['--role','migrate','--expect-env','production'],with_env=True)
            if preflight.returncode:
                raise Stop('Offline migration configuration rejected; details withheld')
            # Keep diagnostics server-local and private. Never echo DB errors,
            # URLs, passwords, row values or SQL logs into the chat/terminal.
            log_path = env_file.with_name('migration-'+owner+'.log')
            result = logged_migration(log_path,lambda:run('migrate',[],network='coolify',with_env=True,migrate=True,timeout=200))
            if result.returncode:
                raise Stop('Migration failed; stop release and review private server log')
            print(json.dumps(dict(plan,migration_command_succeeded=True,approval_ref_recorded=True,
                next_gate='Independently verify ledger/schema before starting any new consumer')))
        finally:
            for name in names:
                check = subprocess.run(['docker','inspect','--format','{{index .Config.Labels "atl.release.owner"}}',name],capture_output=True,text=True,timeout=10)
                if check.returncode == 0 and check.stdout.strip() == owner:
                    subprocess.run(['docker','rm','-f',name],capture_output=True,timeout=15,check=True)


if __name__ == '__main__':
    try:
        main()
    except (ValueError,OSError,KeyError,subprocess.SubprocessError,KeyboardInterrupt) as error:
        # Validation messages are fixed metadata; external process text is never printed.
        print(json.dumps({'stopped':True,'reason':str(error) if isinstance(error,Stop) else type(error).__name__,'production_release_must_not_continue':True}))
        raise SystemExit(1)
