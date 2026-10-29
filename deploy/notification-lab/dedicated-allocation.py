"""Offline plan validation; validate live Docker/cgroup evidence before any load."""
import json
from pathlib import Path
import sys

def cpu_set(value):
    result=set()
    for part in value.split(','):
        bounds=[int(x) for x in part.split('-')]
        assert len(bounds) in (1,2) and bounds[0]>=0
        if len(bounds)==2:
            assert bounds[1]>=bounds[0]
        result.update(range(bounds[0],bounds[-1]+1))
    return result

CRITICAL={'postgres':set(range(4)),'sessions':{4,5},'api1':{6},'api2':{7},
          'gateway':{8},'notification-worker':{9}}

def validate(services, online_cpus, available_memory):
    assert set(range(16)) <= cpu_set(online_cpus), 'Proposed dedicated16-vCPU allocation not available'
    assert available_memory>=20*1024**3, 'Insufficient independent memory headroom'
    used=set()
    for role,expected in CRITICAL.items():
        service=services[role]
        actual=cpu_set(str(service['cpuset']))
        assert actual==expected and not (used & actual), f'Overlapping or incorrect critical CPU allocation: {role}'
        assert service['cgroup_parent']=='atlxpres-lab.slice', 'Unreviewed cgroup parent'
        used |= actual
    assert not (used & set(range(12,16))), 'Workload overlaps controller/OS reserve'
    return {'offline_allocation_valid':True,'critical_cpu_sets_disjoint':True,
            'live_cpu_dedication_verified':False,'capacity_qualified':False}

if __name__=='__main__':
    # Reads a rendered Compose JSON plan, never updates containers or slices.
    rendered=json.loads(Path(sys.argv[1]).read_text())
    print(json.dumps(validate(rendered['services'],sys.argv[2],int(sys.argv[3])),indent=2))
