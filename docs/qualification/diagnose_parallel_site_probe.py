"""Read-only matched probe comparison, not a baseline or capacity qualification."""
import concurrent.futures,json,subprocess,time

URLS=('https://api.atlxpres.com/api/v1/ready','https://unlimitedenergysystems.com/','https://atlxpres.com/')
FIELDS=('http_code','time_namelookup','time_connect','time_appconnect','time_starttransfer','time_total','size_download','remote_ip')
def probe(url,ipv4):
    fmt='{'+','.join('"'+k+'":"%{'+k+'}"' for k in FIELDS)+'}'
    args=['curl','-q','--silent','--show-error','--location','--max-redirs','5','--proto','=https',
          '--proto-redir','=https','--max-time','2','--connect-timeout','2',
          '--user-agent','ATL-Xpress-cohost-safety/1.0','--output','/dev/null','--write-out',fmt]
    if ipv4:args.append('-4')
    started=time.monotonic()
    try:
        result=subprocess.run(args+[url],capture_output=True,text=True,timeout=2.5)
        row=json.loads(result.stdout)
        row.update(url=url,exit_code=result.returncode,error=result.stderr.strip()[:500],elapsed_seconds=round(time.monotonic()-started,6))
        return row
    except Exception as error:return dict(url=url,error_class=type(error).__name__,elapsed_seconds=round(time.monotonic()-started,6))

def main():
    # No DNS warm-up: reproduce the actual three-concurrent-request observer.
    # ABBA order, same two-second deadline and five-second cadence in every arm.
    rows=[]
    for index,ipv4 in enumerate((False,True,True,False)*3):
        started=time.monotonic()
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            samples=list(pool.map(lambda url:probe(url,ipv4),URLS))
        rows.append(dict(round=index+1,mode='ipv4' if ipv4 else 'default',samples=samples))
        time.sleep(max(0,5-(time.monotonic()-started)))
    print(json.dumps(dict(scope='read-only matched concurrent observer comparison; NOT qualification',
        services_changed=False,database_writes=False,load_started=False,deadline_seconds=2,
        request_count=36,rounds=rows),indent=2))

if __name__=='__main__':main()
