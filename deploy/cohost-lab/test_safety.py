import unittest
from safety import reasons, validate_inventory, LABEL
from watchdog import delta

class SafetyTests(unittest.TestCase):
    def sample(self):
        return dict(available_bytes=4*2**30,disk_free_bytes=40*2**30,oom=False,
            production_healthy=True,cpu_high_seconds=0,iowait_high_seconds=0,
            health_failures=0,latency_failures=0,peer_heartbeat_age_seconds=0)
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
