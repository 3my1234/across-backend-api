"""Bounded local SSH status/cleanup calls; retry only a read-only status timeout."""
import argparse,json,subprocess,sys
from pathlib import Path

def execute(private,command):
    if command not in ('setup-status','status','cleanup'):raise ValueError('unreviewed local operator command')
    args=['ssh','-i',str(private/'ci'),'-o','IdentitiesOnly=yes','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+str(private/'known_hosts'),'-o','ConnectTimeout=15','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','root@79.143.176.173',command]
    for attempt in range(2 if command!='cleanup' else 1):
        try:
            result=subprocess.run(args,capture_output=True,timeout=180 if command=='cleanup' else 45,check=True)
            return result.stdout
        except subprocess.TimeoutExpired:
            if command=='cleanup' or attempt:raise
            print('Read-only SSH status timed out; reconnecting once to the SAME server job.',file=sys.stderr,flush=True)
        except subprocess.CalledProcessError as error:
            message=(error.stderr or b'').decode(errors='replace')
            try:(private/'last-ssh-error.json').write_text(json.dumps(dict(command=command,exit_code=error.returncode,kind='authentication_denied' if 'Permission denied' in message else 'transport_or_remote_failure',stderr=message[-2000:])))
            except OSError:pass  # Diagnostics must never replace the original SSH failure.
            raise

def observe(private,command):
    """A missing pre-load observation is unknown, never permission to load."""
    try:
        return execute(private,command)
    except subprocess.TimeoutExpired:
        if command!='setup-status':raise
        return json.dumps(dict(observation_available=False,phase='status-unavailable',ready=False,closed=None,guard_error=None)).encode()

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--private',type=Path,required=True);p.add_argument('--command',required=True);a=p.parse_args()
    try:sys.stdout.buffer.write(observe(a.private,a.command))
    except subprocess.CalledProcessError as error:
        sys.stderr.write((error.stderr or b'').decode(errors='replace'))
        raise SystemExit(error.returncode)
