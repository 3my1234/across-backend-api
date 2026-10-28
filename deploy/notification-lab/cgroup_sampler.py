"""Read-only 100ms Linux cgroup samples for the explicitly isolated CI lab."""
import json
import os
from pathlib import Path
import signal
import subprocess
import time

if os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('LAB_INVESTIGATION_PHASE') != 'system':
    raise SystemExit('Only the isolated system investigation workflow is allowed')

running = True
def stop(*_):
    global running
    running = False
signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)

root = Path('/sys/fs/cgroup').resolve()
containers = {}
refresh = 0
def counters(path):
    return {key: int(value) for key, value in (line.split() for line in path.read_text().splitlines())}

while running:
    began = time.monotonic()
    if began >= refresh:
        ids = subprocess.run(['docker', 'ps', '-q', '--filter', 'label=com.docker.compose.project=atlxpres-capacity-lab'], capture_output=True, text=True, check=True).stdout.split()
        containers = {}
        if ids:
            for item in json.loads(subprocess.run(['docker', 'inspect', *ids], capture_output=True, text=True, check=True).stdout):
                pid = item['State']['Pid']
                try:
                    group = next(line.split('::', 1)[1] for line in Path(f'/proc/{pid}/cgroup').read_text().splitlines() if line.startswith('0::'))
                    path = (root / group.lstrip('/')).resolve()
                    if path.is_relative_to(root) and (path / 'cpu.stat').exists():
                        containers[item['Name'].lstrip('/')] = path
                except (FileNotFoundError, StopIteration):
                    continue
        refresh = began + 2
    sample = {'time_unix': time.time(), 'monotonic': began, 'containers': {}}
    for name, path in containers.items():
        try:
            sample['containers'][name] = {
                'cpu': counters(path / 'cpu.stat'),
                'memory_current': int((path / 'memory.current').read_text()),
                'memory_events': counters(path / 'memory.events'),
                'cpu_max': (path / 'cpu.max').read_text().strip(),
            }
        except FileNotFoundError:
            continue
    sample['host_memory_kib'] = {key.rstrip(':'): int(value.split()[0]) for key, value in (line.split(':', 1) for line in Path('/proc/meminfo').read_text().splitlines()) if key in ('MemAvailable', 'SwapFree')}
    sample['sampler_ms'] = (time.monotonic() - began) * 1000
    print(json.dumps(sample), flush=True)
    time.sleep(max(0, 0.1 - (time.monotonic() - began)))
