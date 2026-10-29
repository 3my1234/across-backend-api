"""Owned-container command adapter. Never accepts Docker contexts, names or env files."""
import json
from pathlib import Path
import re
import subprocess
import sys
from safety import validate_inventory

LAB_COMMANDS={"seed","prepare-sessions","verify-sessions","observe-sessions","observe-system","notification-verify","queue-check","lifecycle"}

def command(manifest,operation):
    action=operation.get("action");service=operation.get("service")
    ids=manifest.get("services",{})
    if service not in ids or ids[service] not in manifest.get("container_ids",[]):
        raise ValueError("unrecorded service")
    ident=ids[service]
    if not re.fullmatch(r"[a-f0-9]{64}",ident):raise ValueError("immutable ID required")
    if action=="identity":
        if (manifest['role'],service) not in (("database","postgres"),("main","db-probe")):
            raise ValueError("test database identity endpoint required")
        connection=[] if manifest['role']=="database" else ['-h','/tunnel','-p','55432']
        return ['docker','exec',ident,'psql',*connection,'-U','lab','-d','capacity_lab','-XAtc',
            "SELECT current_database() || ':' || current_setting('cluster_name')"]
    if action=="start" and service in ("api1","api2","gateway","notification-worker") and manifest.get("load_authorized") is not True:
        raise ValueError("application startup/load not authorized")
    if action in ("start","stop","logs"):
        return ["docker",action,*( ["--time","2"] if action=="stop" else ["--tail","2000"] if action=="logs" else []),ident]
    if action!="exec":raise ValueError("operation denied")
    args=operation.get("args",[])
    if not args or not all(isinstance(x,str) and len(x)<65536 for x in args):raise ValueError("invalid args")
    if args[0]=="/app/lab":
        if service not in ("seed","exporter","verifier","queue-check","lifecycle"):raise ValueError("wrong lab service")
        if len(args)!=2 or args[1] not in LAB_COMMANDS:raise ValueError("unreviewed lab command")
        if args[1]=="seed" and manifest.get("fixture_preparation_authorized") is not True:raise ValueError("fixture preparation not authorized")
    elif args[0]=="psql":
        if service!="postgres" or manifest["role"]!="database":raise ValueError("database host required")
        # Fixed host/database/user; SQL only through stdin. No metacommand or -h override.
        if args!=["psql","-U","lab","-d","capacity_lab","-XAt","-v","ON_ERROR_STOP=1"]:raise ValueError("fixed SQL invocation required")
    elif args[0] in ("pg_dump","pg_restore"):
        expected=(["pg_dump","-U","lab","-Fc","capacity_lab"] if args[0]=="pg_dump" else
                  ["pg_restore","-U","lab","--clean","--if-exists","--exit-on-error","-d","capacity_lab"])
        if service!="postgres" or manifest["role"]!="database" or args!=expected:raise ValueError("fixed disposable snapshot invocation required")
    elif args[0]=="redis-cli":
        if service!="redis" or args not in (["redis-cli","INFO","memory"],["redis-cli","INFO","stats"],["redis-cli","FLUSHDB"]):raise ValueError("test Redis operation denied")
    elif args[0]=="curl":
        allowed=(["curl","-fsS","--max-time","3","http://gateway:8080/api/v1/ready"],
                 ["curl","-fsS","http://localhost:8081/stats"])
        if args not in allowed or service not in ("api1","api2","notification-provider"):raise ValueError("test URL only")
    else:raise ValueError("binary denied")
    return ["docker","exec","-i",ident,*args]


def run(base,operation):
    manifest=json.loads((base/"manifest.json").read_text())
    cmd=command(manifest,operation)
    ident=manifest["services"][operation["service"]]
    inspected=json.loads(subprocess.check_output(["docker","inspect",ident],text=True,timeout=5))
    validate_inventory(dict(run=manifest["run"],container_ids=[ident]),inspected)
    if operation["action"]=="exec" and manifest["role"]=="database":
        marker=subprocess.check_output(["docker","exec",ident,"psql","-U","lab","-d","capacity_lab","-XAtc","SELECT marker FROM capacity_lab_identity"],text=True,timeout=10).strip()
        if marker!="disposable-compose-only":raise ValueError("disposable DB marker missing")
    return subprocess.run(cmd,check=False).returncode

if __name__=="__main__":
    raise SystemExit(run(Path(sys.argv[1]),json.loads(sys.argv[2])))
