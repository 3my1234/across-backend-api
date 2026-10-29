"""Read-only effective cgroup verification; called before any seeding/load."""
import argparse
import json
from pathlib import Path
import subprocess
from safety import validate_inventory


def verify_files(directory, cpu, memory):
    quota,period=(directory/"cpu.max").read_text().split()
    if quota=="max" or int(period)<=0 or int(quota)/int(period)>cpu:
        raise ValueError("test parent CPU cap absent or excessive")
    limit=(directory/"memory.max").read_text().strip()
    if limit=="max" or int(limit)>memory:raise ValueError("test parent memory cap absent or excessive")
    if (directory/"memory.swap.max").read_text().strip()!="0":raise ValueError("swap must be disabled")
    if int((directory/"cpu.weight").read_text())>10:raise ValueError("test CPU weight too high")
    weight=(directory/"io.weight").read_text().splitlines()
    if not weight or any(int(line.split()[-1])>10 for line in weight):raise ValueError("test IO weight too high")
    tasks=(directory/"pids.max").read_text().strip()
    if tasks=="max" or int(tasks)>(2048 if cpu==2 else 512):raise ValueError("test tasks limit absent/excessive")


def main():
    ap=argparse.ArgumentParser();ap.add_argument("manifest",type=Path);a=ap.parse_args()
    m=json.loads(a.manifest.read_text());role=m.get("role")
    if role not in ("main","database"):raise ValueError("invalid role")
    ids=m.get("container_ids",[])
    raw=subprocess.check_output(["docker","inspect",*ids],text=True,timeout=10)
    records=json.loads(raw);validate_inventory(m,records)
    expected=m["run"].replace("-", "")+role+".slice"
    for record in records:
        pid=record["State"].get("Pid",0)
        if pid<=0:raise ValueError("container must be running for ancestry verification")
        memberships=Path(f"/proc/{pid}/cgroup").read_text().splitlines()
        if len(memberships)!=1 or not memberships[0].startswith("0::/"):
            raise ValueError("unified cgroup v2 required")
        relative=Path(memberships[0].split("::",1)[1].lstrip("/"))
        if ".." in relative.parts or expected not in relative.parts:raise ValueError("test parent escaped")
        parent=Path('/sys/fs/cgroup').joinpath(*relative.parts[:relative.parts.index(expected)+1])
        verify_files(parent,2 if role=="main" else 3,(2684354560 if role=="main" else 3221225472))
    print(json.dumps(dict(effective_test_parent="verified",role=role,containers=len(records),dedicated_cpu_guarantee=False)))

if __name__=="__main__":main()
