"""Pinned optimized API overlay and running-executable identity; no deployments."""
import hashlib,json

SOURCE='8b7ebe7d0efe80e045ed1bfd886f742a0f12220a'
REGRESSION_RUN=42343898317

def build_args(destination):
    return ['go','build','-buildvcs=false','-trimpath','-ldflags=-s -w -I /lib/ld-musl-x86_64.so.1','-o',str(destination),'./cmd/api']

def manifest(path):
    from verifier_build import validate_binary
    validate_binary(path)
    return dict(api_source=SOURCE,api_regression_run=REGRESSION_RUN,api_sha256=hashlib.sha256(path.read_bytes()).hexdigest(),api_bytes=path.stat().st_size)

def validate_files(base,approval):
    actual=manifest(base/'api.bin')
    recorded=json.loads((base/'api-artifact.json').read_text())
    if actual!=recorded or any(approval.get(k)!=actual[k] for k in ('api_source','api_regression_run','api_sha256')):
        raise ValueError('optimized API artifact identity mismatch')
    return actual

def verify_running(base,approval):
    from runtime import inventory,call
    expected=validate_files(base,approval)
    m,rows=inventory(base);by_id={r['Id']:r for r in rows};verified={}
    for name in ('api1','api2'):
        ident=m['services'][name];row=by_id[ident]
        mount=[v for v in row.get('Mounts',[]) if v.get('Destination')=='/app/api']
        if row.get('State',{}).get('Running') is not True or row.get('Path')!='/app/api' or len(mount)!=1 or mount[0].get('Source')!=str(base/'api.bin') or mount[0].get('RW') is not False:
            raise ValueError('optimized API process/mount mismatch: '+name)
        digest=call(['docker','exec',ident,'sha256sum','/proc/1/exe'],timeout=15).decode().split()[0]
        if digest!=expected['api_sha256']:raise ValueError('running API executable checksum mismatch: '+name)
        verified[name]=dict(container_id=ident,running_executable_sha256=digest)
    result={**expected,'both_running_apis_verified':True,'services':verified}
    (base/'api-running.json').write_text(json.dumps(result,indent=2))
    return result
