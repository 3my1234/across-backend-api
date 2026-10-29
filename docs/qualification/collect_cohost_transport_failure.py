"""Read-only diagnostics for the exact closed test attempt; no SQL or changes."""
import argparse,json,subprocess
from pathlib import Path

RUN='cohost-ad5c7dfa8bb37a25'
PREFIX=RUN.replace('-','')
def capture(args):
    r=subprocess.run(args,capture_output=True,text=True,timeout=15)
    return r.returncode,r.stdout,r.stderr

def main():
    p=argparse.ArgumentParser();p.add_argument('--role',choices=['main','database'],required=True);a=p.parse_args()
    base=Path('/root/atlxpres-cohost')/RUN
    report=dict(run=RUN,role=a.role,services_changed=False,database_writes=False,load_started=False)
    report['cleanup_complete']=(base/'cleanup.complete').exists()
    code,stdout,stderr=capture(['docker','ps','-aq','--filter','label=atlxpres.cohost.run='+RUN])
    report['remaining_test_container_count']=len(stdout.split()) if code==0 else None
    keys=Path('/root/.ssh/authorized_keys')
    report['remaining_test_key_lines']=sum(RUN in line for line in keys.read_text().splitlines()) if keys.exists() else None
    report['unit_diagnostics']={}
    for suffix in (['dbtunnel.service','mainguard.service','mainreaper.service'] if a.role=='main' else ['databaseguard.service','databasereaper.service']):
        unit=PREFIX+suffix
        code,stdout,stderr=capture(['journalctl','--no-pager','--output=cat','-u',unit,'-n','100'])
        # Only this attempt's dedicated units; never auth logs, envs or keys.
        report['unit_diagnostics'][suffix]=dict(exit_code=code,lines=stdout.splitlines()[-100:])
    print(json.dumps(report,indent=2))

if __name__=='__main__':main()
