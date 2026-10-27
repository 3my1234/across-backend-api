"""Observe only containers belonging to the fixed disposable Compose project."""
import datetime
import json
import subprocess
import sys
import time

PROJECT = 'atlxpres-capacity-lab'

def command(*args):
    return subprocess.check_output(list(args), text=True, timeout=20).strip()

def stamp():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()

def snapshot():
    result = {'at': stamp(), 'cpu_usage_usec': {}}
    for role in ('postgres', 'api1', 'api2'):
        name = f'{PROJECT}-{role}-1'
        fields = dict(line.split() for line in command('docker', 'exec', name, 'cat', '/sys/fs/cgroup/cpu.stat').splitlines())
        result['cpu_usage_usec'][role] = int(fields['usage_usec'])
    return result

if __name__ == '__main__':
    if sys.argv[1:] == ['snapshot']:
        print(json.dumps(snapshot()))
    elif sys.argv[1:] == ['sample']:
        while True:
            began = stamp()
            try:
                ids = command('docker', 'ps', '--filter', f'label=com.docker.compose.project={PROJECT}', '--format', '{{.ID}}').splitlines()
                if not ids:
                    raise RuntimeError('no disposable containers')
                rows = [json.loads(line) for line in command('docker', 'stats', '--no-stream', '--format', '{{json .}}', *ids).splitlines()]
                print(json.dumps({'started_at': began, 'ended_at': stamp(), 'containers': rows}), flush=True)
            except (OSError, subprocess.SubprocessError, ValueError, RuntimeError) as error:
                print(json.dumps({'started_at': began, 'sampling_error': type(error).__name__}), flush=True)
            time.sleep(3)
    else:
        raise SystemExit('Use snapshot or sample; no arbitrary project/target accepted')
