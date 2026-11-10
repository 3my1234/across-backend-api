"""Reuse verified HTTPS connections under the original2s/2.5s safety deadlines.

Uses the host's existing libcurl; no package installation or relaxed TLS.
ABI constants: curl8.5.0 include/curl/curl.h, upstream curl repository.
Each URL owns one bounded worker inside its guardian's existing cgroup.
"""
import atexit,ctypes as C,ctypes.util,json,math,multiprocessing,threading,time

URLS=('https://api.atlxpres.com/api/v1/ready','https://atlxpres.com/')

class Curl:
    def __init__(self,url):
        if url not in URLS:raise ValueError('fixed production health URL required')
        self.url=url;self.ready=url.endswith('/ready');self.body=bytearray();self.downloaded=0
        path=ctypes.util.find_library('curl')
        if not path:raise RuntimeError('existing libcurl unavailable')
        self.lib=C.CDLL(path)
        signatures={'curl_global_init':([C.c_long],C.c_int),'curl_easy_init':([],C.c_void_p),'curl_easy_setopt':([C.c_void_p,C.c_int],C.c_int),'curl_easy_getinfo':([C.c_void_p,C.c_int],C.c_int),'curl_easy_perform':([C.c_void_p],C.c_int),'curl_easy_cleanup':([C.c_void_p],None)}
        for name,(args,result) in signatures.items():
            fn=getattr(self.lib,name);fn.argtypes=args;fn.restype=result
        if self.lib.curl_global_init(3):raise RuntimeError('libcurl initialization failed')
        self.handle=self.lib.curl_easy_init()
        if not self.handle:raise RuntimeError('libcurl handle unavailable')
        self.error=C.create_string_buffer(256)
        self.callback=C.CFUNCTYPE(C.c_size_t,C.c_void_p,C.c_size_t,C.c_size_t,C.c_void_p)(self.write)
        self.references=[]
        try:
            for option,value in ((155,2000),(156,2000),(52,1),(68,5),(99,1),(64,1),(81,2)):
                self.set(option,C.c_long(value))
            for option,value in ((10002,url),(10018,'ATL-Xpress-cohost-safety/1.0'),(10318,'https'),(10319,'https')):
                data=C.c_char_p(value.encode());self.references.append(data);self.set(option,data)
            self.set(10010,self.error);self.set(20011,self.callback)
        except BaseException:self.close();raise
    def set(self,option,value):
        if self.lib.curl_easy_setopt(self.handle,option,value):raise RuntimeError('required libcurl safety option rejected: '+str(option))
    def write(self,data,size,count,_):
        length=size*count;self.downloaded+=length
        if self.ready:
            if len(self.body)+length>8192:return 0
            self.body.extend(C.string_at(data,length))
        return length
    def info(self,key,kind):
        value=kind()
        if self.lib.curl_easy_getinfo(self.handle,key,C.byref(value)):raise RuntimeError('complete HTTP timing metadata required')
        return value.value
    def request(self):
        self.body.clear();self.downloaded=0;self.error[0]=0
        code=self.lib.curl_easy_perform(self.handle)
        status=self.info(0x200002,C.c_long)
        phases={name:self.info(0x300000+key,C.c_double) for name,key in (('dns_seconds',4),('connect_seconds',5),('tls_seconds',33),('first_byte_seconds',17),('total_seconds',3))}
        phases['download_bytes']=self.downloaded
        valid=code==0 and status==200
        if self.ready:
            try:valid=valid and len(self.body)<=8192 and json.loads(self.body).get('ok') is True
            except (ValueError,AttributeError,UnicodeError):valid=False
        return dict(url=self.url,exit_code=code,http_status=str(status),error=self.error.value.decode(errors='replace')[:500],phases=phases,transport_healthy=valid,http_seconds=phases['total_seconds'],new_connections=self.info(0x20001a,C.c_long),probe_transport='persistent-verified-libcurl')
    def close(self):
        if self.handle:self.lib.curl_easy_cleanup(self.handle);self.handle=None

def worker(pipe,url):
    engine=None
    try:
        engine=Curl(url)
        while pipe.recv()=='probe':pipe.send(engine.request())
    except EOFError:pass
    except Exception as error:
        try:pipe.send(dict(healthy=False,error_class=type(error).__name__,error=str(error)))
        except (EOFError,OSError):pass
    finally:
        if engine:engine.close()
        pipe.close()

def accepted(detail,observer_seconds):
    seconds=detail.get('http_seconds')
    return (detail.get('transport_healthy') is True and isinstance(seconds,(int,float)) and math.isfinite(seconds) and 0<seconds<=2 and math.isfinite(observer_seconds) and 0<=observer_seconds<=2.5)

class Observer:
    def __init__(self,url):
        if url not in URLS:raise ValueError('fixed production health URL required')
        self.url=url;self.process=None;self.pipe=None;self.lock=threading.Lock()
    def close(self):
        if self.process:
            if self.process.is_alive():self.process.kill()
            self.process.join(timeout=.1)
        if self.pipe:self.pipe.close()
        self.process=None;self.pipe=None
    def probe(self):
        with self.lock:
            start=time.monotonic();detail=dict(url=self.url,probe_transport='persistent-verified-libcurl')
            try:
                if self.process is None or not self.process.is_alive():
                    self.close();context=multiprocessing.get_context('spawn');self.pipe,child=context.Pipe()
                    self.process=context.Process(target=worker,args=(child,self.url),daemon=True);self.process.start();child.close()
                self.pipe.send('probe')
                remaining=2.5-(time.monotonic()-start)
                if remaining<=0 or not self.pipe.poll(remaining):
                    detail['process_timeout']=True;raise TimeoutError('unchanged2.5second observer deadline')
                detail.update(self.pipe.recv())
                elapsed=time.monotonic()-start
                detail.update(observer_seconds=elapsed,seconds=elapsed,healthy=accepted(detail,elapsed),timing_policy='http-2s-observer-2.5s-v1')
                return detail
            except Exception as error:
                self.close();detail.update(healthy=False,error_class=type(error).__name__,seconds=time.monotonic()-start,timing_policy='http-2s-observer-2.5s-v1')
                return detail

observers={};lock=threading.Lock()
def close():
    for observer in observers.values():observer.close()
atexit.register(close)

def probe(url,diagnostics=None):
    with lock:
        if url not in observers:observers[url]=Observer(url)
        observer=observers[url]
    detail=observer.probe()
    if diagnostics is not None:diagnostics[url]=detail
    return url,detail['healthy'],detail.get('http_seconds',detail['seconds'])
