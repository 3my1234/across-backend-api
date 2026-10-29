"""Fail-closed resource check; no provisioning, database access or runtime changes."""
import json
import os
from pathlib import Path

def validate(cpus, available_bytes, isolated):
    assert isolated, 'An explicitly reviewed isolated non-production environment is required'
    assert cpus >= 12, 'Full concurrent quota budget needs at least12 available logical CPUs'
    assert available_bytes >= 20*1024**3, 'Require20GiB available RAM before preparing the isolated stack'
    return {'environment_only':True,'available_cpus':cpus,'available_memory_bytes':available_bytes,
            'database_cpu_budget':4,'database_memory_bytes':8*1024**3,
            'generator_cpu_budget':2,'generator_memory_bytes':4*1024**3,
            'gateway_cpu_budget':1,'capacity_qualified':False}

if __name__ == '__main__':
    assert os.environ.get('GITHUB_ACTIONS') == 'true', 'Existing isolated CI only; do not execute on production'
    cores=len(os.sched_getaffinity(0))
    quota=Path('/sys/fs/cgroup/cpu.max')
    if quota.exists():
        budget,period=quota.read_text().split()
        if budget!='max':cores=min(cores,int(budget)/int(period))
    available=int(next(x.split()[1] for x in Path('/proc/meminfo').read_text().splitlines() if x.startswith('MemAvailable:')))*1024
    print(json.dumps(validate(cores,available,os.environ.get('LAB_REPRESENTATIVE_ENVIRONMENT')=='reviewed-isolated-only'),indent=2))
