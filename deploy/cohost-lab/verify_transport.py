"""Operator setup-only identity check through the actual main-to-DB test tunnel."""
import argparse,json,subprocess,time
from pathlib import Path
from control import command
from safety import validate_inventory


def main():
    ap=argparse.ArgumentParser();ap.add_argument('manifest',type=Path);a=ap.parse_args()
    m=json.loads(a.manifest.read_text())
    if m.get('role')!='main' or m.get('load_authorized') is not False:raise ValueError('setup-only main inventory required')
    for service in ('postgres','db-probe'):
        ident=m['services'][service]
        row=json.loads(subprocess.check_output(['docker','inspect',ident],text=True))
        validate_inventory(dict(run=m['run'],container_ids=[ident]),row)
        subprocess.run(command(m,dict(action='start',service=service)),check=True)
    subprocess.run(['python3',str(Path(__file__).with_name('verify_cgroup.py')),str(a.manifest)],check=True)
    deadline=time.monotonic()+5
    while True:
        try:
            result=subprocess.check_output(command(m,dict(action='identity',service='db-probe')),text=True,timeout=10).strip()
            break
        except subprocess.CalledProcessError:
            if time.monotonic()>=deadline:raise
            time.sleep(.2)
    if result!='capacity_lab:'+m['run']:raise ValueError('unexpected test database destination')
    print(json.dumps(dict(actual_main_to_test_database='passed',identity=result,production_database_accessed=False,application_load_started=False)))

if __name__=='__main__':main()
