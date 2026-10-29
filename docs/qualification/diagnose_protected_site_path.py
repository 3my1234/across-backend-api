"""Bounded read-only public-site timing. No setup, SQL or configuration changes."""
import argparse,json,subprocess,time,urllib.parse
from pathlib import Path

SITE='https://unlimitedenergysystems.com/'
FIELDS=('http_code','time_namelookup','time_connect','time_appconnect','time_starttransfer',
        'time_total','time_redirect','size_download','num_redirects','remote_ip','http_version','url_effective')

def sample(name,url=SITE,extra=()):
    fmt='{'+','.join('"'+key+'":"%{'+key+'}"' for key in FIELDS)+'}'
    args=['curl','-q','--silent','--show-error','--location','--max-redirs','5','--proto','=https',
          '--proto-redir','=https','--max-time','2','--connect-timeout','2',
          '--user-agent','ATL-Xpress-cohost-safety/1.0','--output','/dev/null','--write-out',fmt]
    started=time.monotonic()
    try:
        result=subprocess.run(args+list(extra)+[url],capture_output=True,text=True,timeout=3)
        row=json.loads(result.stdout)
        effective=urllib.parse.urlsplit(row.pop('url_effective',''))
        row.update(name=name,exit_code=result.returncode,elapsed_seconds=round(time.monotonic()-started,6),
            effective_host=effective.hostname,effective_path=effective.path,diagnostic_error=result.stderr.strip()[:500])
        return row
    except Exception as error:return dict(name=name,error_class=type(error).__name__,elapsed_seconds=round(time.monotonic()-started,6))

def main():
    p=argparse.ArgumentParser();p.add_argument('--role',choices=['main','database'],required=True);a=p.parse_args()
    report=dict(role=a.role,services_changed=False,database_writes=False,load_started=False,limit_seconds=2)
    try:
        dns=subprocess.run(['getent','ahosts','unlimitedenergysystems.com'],capture_output=True,text=True,timeout=10)
        addresses=sorted(set(line.split()[0] for line in dns.stdout.splitlines() if line.split()))
        report['resolved_addresses']=addresses
    except Exception as error:addresses=[];report['dns_error_class']=type(error).__name__
    report['samples']=[sample('api_control','https://api.atlxpres.com/api/v1/ready'),sample('landing_control','https://atlxpres.com/')]
    for index in range(3):
        report['samples'].append(sample('normal_'+str(index+1)))
        time.sleep(2)
    for name,extra in [('ipv4',('-4',)),('ipv6',('-6',)),('http1',('--http1.1',)),('headers_only_diagnostic',('--head',))]:
        report['samples'].append(sample(name,extra=extra))
    ipv4=next((ip for ip in addresses if ':' not in ip),None)
    if ipv4:report['samples'].append(sample('pinned_current_dns_ipv4',extra=('--resolve','unlimitedenergysystems.com:443:'+ipv4)))
    base=Path('/root/atlxpres-cohost/cohost-0cfe085cbad1ec57')
    report['setup_manifest_exists']=(base/'manifest.json').exists()
    if (base/'baseline-failure.json').exists():report['retained_failure']=json.loads((base/'baseline-failure.json').read_text())
    print(json.dumps(report,indent=2))

if __name__=='__main__':main()
