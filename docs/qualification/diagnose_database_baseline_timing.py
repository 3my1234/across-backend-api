"""Small read-only timing sample. No SQL, settings, setup or load generation."""
import concurrent.futures,json,ssl,subprocess,time,urllib.request,urllib.error
from pathlib import Path

URLS=('https://api.atlxpres.com/api/v1/ready','https://unlimitedenergysystems.com/','https://atlxpres.com/')
UA='ATL-Xpress-cohost-safety/1.0'
def python_probe(url):
    started=time.monotonic();row=dict(url=url)
    try:
        with urllib.request.urlopen(urllib.request.Request(url,headers={'User-Agent':UA}),timeout=2) as response:
            body=response.read(8192);row['status']=response.status
            if url.endswith('/ready'):row['ready']=json.loads(body).get('ok') is True
    except urllib.error.HTTPError as error:row.update(status=error.code,error_class='HTTPError')
    except Exception as error:row['error_class']=type(error).__name__
    row['total_seconds']=round(time.monotonic()-started,6)
    return row

def curl_probe(url,ipv4=False):
    fields={'dns_seconds':'time_namelookup','connect_seconds':'time_connect','tls_seconds':'time_appconnect',
            'first_byte_seconds':'time_starttransfer','total_seconds':'time_total','http_status':'http_code'}
    fmt='{'+','.join('"'+k+'":%{'+v+'}' for k,v in fields.items())+'}'
    # http_code can be 000 on failure, which isn't valid JSON. Parse as strings.
    fmt='{'+','.join('"'+k+'":"%{'+v+'}"' for k,v in fields.items())+'}'
    args=['curl','-q','-sS','--connect-timeout','2','--max-time','6','-A',UA,'-o','/dev/null','-w',fmt]
    if ipv4:args.append('-4')
    try:
        result=subprocess.run(args+[url],capture_output=True,text=True,timeout=8)
        row=json.loads(result.stdout);row.update(url=url,ipv4_only=ipv4,exit_code=result.returncode)
        return row
    except Exception as error:return dict(url=url,ipv4_only=ipv4,error_class=type(error).__name__)

report=dict(scope='bounded database-host public-path timing only',services_changed=False,database_writes=False,load_started=False)
report['python_samples']=[]
for index in range(3):
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        report['python_samples'].append(list(pool.map(python_probe,URLS)))
    if index<2:time.sleep(2)
report['curl_samples']=[curl_probe(url) for url in URLS]
report['curl_ipv4_api']=curl_probe(URLS[0],True)
started=time.monotonic();context=ssl.create_default_context()
report['tls_context_initialization_seconds']=round(time.monotonic()-started,6)
report['proxy_configured']=bool(urllib.request.getproxies())
base=Path('/root/atlxpres-cohost/cohost-6d6c5f9220874022')
report['manifest_exists']=(base/'manifest.json').exists()
for name in ('baseline-failure.json','baseline.json'):
    if (base/name).exists():
        try:report[name]=json.loads((base/name).read_text())
        except Exception as error:report[name]={'error_class':type(error).__name__}
print(json.dumps(report,indent=2))
