"""Additional shared host ceiling. Component limits and qualification gates stay fixed."""
import re
from pathlib import Path

SHARED_MEMORY=4*2**30
OUTSIDE_MONITORS=256*2**20
def shared(run):
    if not re.fullmatch('cohost-[a-f0-9]{16}',run):raise ValueError('invalid owned budget nonce')
    return run.replace('-','')+'native.slice'
def app(run,role,native):
    return shared(run).removesuffix('.slice')+'-main.slice' if native and role=='main' else run.replace('-','')+role+'.slice'
def directory(run,role,native):
    root=Path('/sys/fs/cgroup')
    if native and role=='main':root/=shared(run)
    return root/app(run,role,native)
def setup(run):
    return [
        ['systemd-run','--unit',run.replace('-','')+'nativeanchor.service','--slice',shared(run),'/bin/sleep','infinity'],
        ['systemctl','set-property','--runtime',shared(run),'MemoryMax='+str(SHARED_MEMORY),'MemorySwapMax=0','CPUQuota=350%','CPUWeight=10','IOWeight=10','TasksMax=4096']]
def additional_required(available,current):
    # Available RAM already excludes resident prepared-test memory. Do not count
    # that usage twice. The outside watchdogs retain their full256MiB allowance.
    required=max(0,SHARED_MEMORY-current)+2*2**30+OUTSIDE_MONITORS
    if available<required:raise ValueError('shared4GiB native budget plus production/monitor reserve does not fit')
    return required
