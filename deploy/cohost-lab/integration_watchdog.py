"""DISPOSABLE CI ONLY: real stop commands, simulated sensor failures, no live addresses."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import watchdog as w


def call(*args): return subprocess.check_output(args,text=True).strip()

def scenario(case,sentinel):
    nonce="cohost-"+"c"*16
    owned=call("docker","run","-d","--network","none","--label","atlxpres.cohost.run="+nonce,"alpine:3.23","sleep","300")
    original=(w.Path,w.cpu,w.probe,w.state,w.time.sleep,w.time.monotonic,w.shutil.disk_usage,sys.argv)
    try:
        with tempfile.TemporaryDirectory() as directory:
            base=Path(directory);manifest=base/"manifest.json"
            manifest.write_text(json.dumps(dict(run=nonce,role="main",container_ids=[owned],production_container_ids=[sentinel])))
            (base/"baseline.json").write_text(json.dumps({u:.01 for u in w.URLS}))
            if case!="peer-loss": (base/"peer.lease").touch()
            class FakeProc:
                def __init__(self,text): self.text=text
                def read_text(self): return self.text
            def mapped(value):
                if str(value)=="/proc/meminfo":
                    available=0 if case=="memory" else 4*2**20
                    return FakeProc("MemAvailable: "+str(available)+" kB\n")
                if str(value)=="/proc/vmstat": return FakeProc("oom_kill 0\n")
                return Path(value)
            ticks=[0]
            def cpu():
                ticks[0]+=100
                return [ticks[0],0,0,0 if case=="cpu" else ticks[0]*9,0,0,0,0]
            def monotonic():
                monotonic.tick+=1
                return monotonic.tick
            monotonic.tick=0
            real_state=w.state
            def states(ids):
                if case=="collector-error": raise OSError("simulated collector failure")
                result=real_state(ids)
                if case=="production-health":result[0]["Running"]=False
                return result
            w.Path=mapped;w.cpu=cpu;w.state=states
            w.probe=lambda u:(u,case!="public-health",.01)
            w.time.sleep=lambda _:None;w.time.monotonic=monotonic
            from collections import namedtuple
            Disk=namedtuple("Disk","total used free")
            w.shutil.disk_usage=lambda _:Disk(80*2**30,40*2**30,40*2**30)
            sys.argv=["watchdog.py",str(manifest),"--stop-test"]
            try:w.main()
            except (SystemExit,OSError):pass
            else:raise AssertionError("watchdog did not terminate")
            assert call("docker","inspect","-f","{{.State.Running}}",owned)=="false",case
            assert call("docker","inspect","-f","{{.State.Running}}",sentinel)=="true",case
            assert not (base/"local.lease").exists(),case
            print("PASS watchdog integration:",case,flush=True)
    finally:
        w.Path,w.cpu,w.probe,w.state,w.time.sleep,w.time.monotonic,w.shutil.disk_usage,sys.argv=original
        subprocess.run(["docker","rm","-f",owned],check=True,stdout=subprocess.DEVNULL)

if __name__=="__main__":
    assert os.environ.get("GITHUB_ACTIONS")=="true" and os.geteuid()==0,"disposable Linux CI only"
    sentinel=call("docker","run","-d","--network","none","alpine:3.23","sleep","300")
    try:
        for case in ("memory","cpu","peer-loss","production-health","public-health","collector-error"):
            scenario(case,sentinel)
    finally:subprocess.run(["docker","rm","-f",sentinel],check=True,stdout=subprocess.DEVNULL)
