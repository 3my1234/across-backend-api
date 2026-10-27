"""Read CPU cgroups only from fixed disposable lab containers; no production."""
import concurrent.futures
import datetime
import json
import subprocess
import time

names = ('atlxpres-capacity-lab-api1-1', 'atlxpres-capacity-lab-api2-1',
         'atlxpres-capacity-lab-postgres-1', 'atlxpres-login-bursts')


def sample(name):
    result = subprocess.run(['docker', 'exec', name, 'cat', '/sys/fs/cgroup/cpu.stat'],
                            capture_output=True, text=True, timeout=3)
    row = {'container': name, 'time': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    if result.returncode == 0:
        row['cpu'] = {line.split()[0]: int(line.split()[1]) for line in result.stdout.splitlines()}
    else:
        row['not_running'] = True
    return row


with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
    while True:
        for row in pool.map(sample, names):
            print(json.dumps(row), flush=True)
        time.sleep(.25)
