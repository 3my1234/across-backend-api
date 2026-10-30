import json,sys,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
import watchdog as w


class CadenceTests(unittest.TestCase):
    def exercise(self, unhealthy=False):
        clock=[0.0];published=[];cycles=[0]
        real_path=Path;real_touch=Path.touch
        class Proc:
            def __init__(self,text):self.text=text
            def read_text(self):return self.text
        def path(value):
            if str(value)=='/proc/meminfo':return Proc('MemAvailable: 8388608 kB\n')
            if str(value)=='/proc/vmstat':return Proc('oom_kill 0\n')
            return real_path(value)
        class Pool:
            def __init__(self,**kwargs):pass
            def __enter__(self):return self
            def __exit__(self,*args):pass
            def map(self,fn,urls):
                # Three HTTP probes run concurrently, each within the approved
                # HTTP/observer deadlines; the complete batch costs2.4seconds.
                cycles[0]+=1
                if cycles[0]>3:raise OSError('end deterministic scan')
                clock[0]+=2.4
                return [(u,not unhealthy,1.9) for u in urls]
        def touch(p,*args,**kwargs):
            if p.name=='local.lease':published.append(clock[0])
            return real_touch(p,*args,**kwargs)
        def sleep(seconds):clock[0]+=seconds
        def cpu():return [int(clock[0]*10),0,0,int(clock[0]*90),0,0,0,0]
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp);manifest=base/'manifest.json'
            manifest.write_text(json.dumps(dict(run='cohost-'+'a'*16,role='main',load_authorized=False,production_container_ids=['b'*64],container_ids=[])))
            (base/'baseline.json').write_text('{}')
            with patch.object(sys,'argv',['watchdog',str(manifest),'--setup-only']),patch('baseline.validate',return_value={u:1 for u in w.URLS}),patch.object(w,'Path',side_effect=path),patch.object(w,'ThreadPoolExecutor',Pool),patch.object(w,'cpu',side_effect=cpu),patch.object(w,'state',return_value=[{'Running':True}]),patch.object(w.shutil,'disk_usage',return_value=type('Disk',(),{'free':50*2**30})()),patch.object(w.time,'monotonic',side_effect=lambda:clock[0]),patch.object(w.time,'sleep',side_effect=sleep),patch.object(Path,'touch',touch),patch.object(w.subprocess,'run'):
                with self.assertRaises((OSError,SystemExit)):w.main()
            self.assertFalse((base/'local.lease').exists())
            rows=[json.loads(x) for x in (base/'watchdog.jsonl').read_text().splitlines()]
        return published,rows

    def test_slow_valid_scans_do_not_add_one_second_or_publish_mid_scan(self):
        stamps,rows=self.exercise()
        self.assertEqual(len(stamps),3)
        self.assertAlmostEqual(stamps[0],3.4)
        self.assertTrue(all(2.39<later-earlier<2.41 for earlier,later in zip(stamps,stamps[1:])))
        self.assertTrue(all(row['sample']['health_failures']==0 for row in rows))

    def test_two_failed_scans_still_stop_and_do_not_publish_a_healthy_lease(self):
        stamps,rows=self.exercise(unhealthy=True)
        self.assertEqual(len(stamps),1)
        self.assertEqual(rows[-1]['sample']['health_failures'],2)
