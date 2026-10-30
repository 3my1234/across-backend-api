import unittest
from safety import reasons, validate_inventory, LABEL
from watchdog import delta
from relay import validate as validate_peers
from access import handle as access_handle
from control import command as container_command
from stage_host import plan as setup_plan
from baseline import validate as validate_baseline
from install_keys import lines as key_lines
from watchdog import URLS
import json
from verify_cgroup import verify_files
from pathlib import Path
import tempfile

class SafetyTests(unittest.TestCase):
    def sample(self):
        return dict(available_bytes=4*2**30,disk_free_bytes=40*2**30,oom=False,
            production_healthy=True,cpu_high_seconds=0,iowait_high_seconds=0,
            health_failures=0,latency_failures=0,peer_heartbeat_age_seconds=0)
    def test_key_scopes_have_expiry_and_only_test_port(self):
        with tempfile.TemporaryDirectory() as d:
            base=Path(d)
            (base/"request.json").write_text(json.dumps(dict(run="cohost-"+"a"*16,role="database",key_expiry_utc="20261030235959Z")))
            for k in ("ci-database","main-database"):(base/(k+".pub")).write_text("ssh-ed25519 AAAA")
            result=key_lines(base)
            self.assertNotIn("port-forwarding",result[0]);self.assertIn("127.0.0.1:55432",result[1])
            self.assertIn("expiry-time=",result[1]);self.assertNotIn("127.0.0.1:5432",result[1])
    def test_baseline_must_be_fresh_complete_and_five_minutes(self):
        d=dict(timing_policy='http-2s-observer-2.5s-v1',observed_seconds=300,finished_unix=1000,urls={u:dict(successful_samples=60,p95_seconds=.2,observer_p95_seconds=.2) for u in URLS})
        self.assertTrue(validate_baseline(d,1001))
        with self.assertRaises(ValueError):validate_baseline(d,2000)
        d["observed_seconds"]=30
        with self.assertRaises(ValueError):validate_baseline(d,1001)
    def test_setup_plan_contains_no_seed_api_worker_or_load(self):
        run="cohost-"+"a"*16
        service=dict(image="sha256:"+"b"*64,cgroup_parent=run.replace("-","")+"main.slice",labels={"atlxpres.cohost.run":run})
        commands=setup_plan(dict(run=run,role="main"),dict(name=run+"-main",services={"redis":service}))
        self.assertEqual(commands[-1][-2:],["start","redis"])
        self.assertIn("--pull",commands[-2]);self.assertIn("never",commands[-2])
    def test_control_rejects_shell_production_and_db_override(self):
        m=dict(role="database",services={"postgres":"a"*64},container_ids=["a"*64])
        for op in (dict(action="exec",service="postgres",args=["sh"]),dict(action="exec",service="production",args=["psql"]),dict(action="exec",service="postgres",args=["psql","-d","across_db"]),dict(action="rm",service="postgres")):
            with self.assertRaises(ValueError):container_command(m,op)
        self.assertEqual(container_command(m,dict(action="stop",service="postgres"))[-1],"a"*64)
    def test_relay_requires_both_fresh_advancing_peers(self):
        run="cohost-"+"a"*16
        good=[dict(run=run,role=r,age_seconds=1,sequence=10) for r in ("main","database")]
        self.assertEqual(validate_peers(good,{},run),{"main":10,"database":10})
        with self.assertRaises(ValueError):validate_peers(good,{"main":10},run)
        good[1]["age_seconds"]=999
        with self.assertRaises(ValueError):validate_peers(good,{},run)
    def test_forced_access_refuses_shell_and_wrong_run(self):
        with tempfile.TemporaryDirectory() as directory:
            base=Path(directory)/("cohost-"+"a"*16);base.mkdir()
            (base/"manifest.json").write_text(json.dumps(dict(run=base.name,role="main")))
            with self.assertRaises(ValueError):access_handle(base,"sh -c whoami")
            access_handle(base,"lease");self.assertTrue((base/"peer.lease").exists())
            self.assertEqual(json.loads(access_handle(base,"status"))["age_seconds"],999)
    def test_parent_cgroup_caps_and_rejection(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)
            values={"cpu.max":"200000 100000","memory.max":"2684354560","memory.swap.max":"0","cpu.weight":"10","io.weight":"default 10","pids.max":"2048"}
            for k,v in values.items():(p/k).write_text(v)
            verify_files(p,2,2684354560)
            for k,bad in {"cpu.max":"max 100000","memory.max":"max","memory.swap.max":"1","cpu.weight":"100","io.weight":"default 100","pids.max":"max"}.items():
                (p/k).write_text(bad)
                with self.assertRaises(ValueError):verify_files(p,2,2684354560)
                (p/k).write_text(values[k])
    def test_cpu_counter_math(self):
        self.assertEqual(delta([0]*8,[10,0,10,70,10,0,0,0]),(20.0,10.0))
        with self.assertRaises(ValueError): delta([1]*8,[0]*8)
    def test_healthy(self): self.assertEqual(reasons(self.sample(),"main"),[])
    def test_short_spike_does_not_abort(self):
        s=self.sample();s["cpu_high_seconds"]=1
        self.assertEqual(reasons(s,"main"),[])
    def test_sustained_cpu_abort(self):
        s=self.sample();s["cpu_high_seconds"]=5
        self.assertTrue(reasons(s,"main"))
    def test_missing_observation_aborts(self): self.assertTrue(reasons({},"main"))
    def test_each_critical_signal(self):
        for key,value in dict(available_bytes=0,disk_free_bytes=0,oom=True,
          production_healthy=False,iowait_high_seconds=5,health_failures=2,
          latency_failures=3,peer_heartbeat_age_seconds=6).items():
            with self.subTest(key=key):
                s=self.sample();s[key]=value;self.assertTrue(reasons(s,"main"))
    def test_invalid_numbers_abort(self):
        for value in (float("nan"),float("inf"),-1,"healthy"):
            s=self.sample();s["available_bytes"]=value
            self.assertTrue(reasons(s,"main"))
    def test_only_owned_full_ids(self):
        ident="a"*64;nonce="cohost-"+"b"*16
        m=dict(run=nonce,container_ids=[ident]);r=[dict(Id=ident,Config=dict(Labels={LABEL:nonce}))]
        self.assertEqual(validate_inventory(m,r),[ident])
        r[0]["Config"]["Labels"][LABEL]="production"
        with self.assertRaises(ValueError): validate_inventory(m,r)
    def test_short_id_rejected(self):
        with self.assertRaises(ValueError): validate_inventory(dict(run="cohost-"+"b"*16,container_ids=["api1"]),[])
    def test_duplicate_id_rejected(self):
        with self.assertRaises(ValueError): validate_inventory(dict(run="cohost-"+"b"*16,container_ids=["a"*64]*2),[])
if __name__ == "__main__": unittest.main()
