import copy,sys,time,unittest,tempfile
from unittest.mock import patch
from pathlib import Path
from policy import validate
from runtime import slices,observations
from runner import SingleHostRunner
from supervise import require_live_fixture
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'cohost-lab'))
from trial_policy import STAGES

class PolicyTests(unittest.TestCase):
    def test_post_abort_evidence_does_not_require_a_live_docker_sensor(self):
        with tempfile.TemporaryDirectory() as folder:
            base=Path(folder);(base/'watchdog-error.json').write_text('{"error_class":"TimeoutExpired"}')
            (base/'ci.key').write_text('must-not-be-exposed')
            with patch('runtime.call',side_effect=AssertionError('must not query stalled Docker')):
                result=observations(base,full=True)
            self.assertIn('TimeoutExpired',result['watchdog-error.json'])
            self.assertNotIn('must-not-be-exposed',str(result))
    def test_foundation_fixture_cannot_start_a_live_qualification(self):
        doc={'services':{name:{'environment':{'LAB_ACTOR_PAIRS':'500','LAB_SCENARIO':'large-busy'}} for name in ('seed','exporter')}}
        require_live_fixture(doc)
        doc['services']['seed']['environment']['LAB_ACTOR_PAIRS']='10'
        with self.assertRaisesRegex(ValueError,'unchanged500-pair'):require_live_fixture(doc)
    def approval(self):
        return dict(run='cohost-'+'a'*16,topology='singlehost-database-vps',host='79.143.176.173',authorized=True,spending_cap_usd=0,
            aggregate_cpu=3,aggregate_memory=5905580032,application_cpu=1,application_memory=2684354560,
            database_cpu=2,database_memory=3221225472,generator_cpu=2,generator_memory=4294967296,lifetime_seconds=7200,
            timing_policy='http-2s-observer-2.5s-v1',approved_unix=100,stages=[list(s) for s in STAGES],source_commit='b'*40,compose_sha256='c'*64,image_archive_sha256='d'*64,verifier_sha256='e'*64,snapshot_sha256='f'*64)
    def test_changed_placement_needs_explicit_current_approval(self):
        a=self.approval();validate(a,now=100)
        for key,value in (('authorized',False),('database_cpu',3),('database_cpu',1),('application_cpu',2),('aggregate_cpu',4),('host','173.212.219.169'),('spending_cap_usd',1)):
            bad={**a,key:value}
            with self.subTest(key=key),self.assertRaises(ValueError):validate(bad,now=100)
        with self.assertRaises(ValueError):validate(a,now=86501)
    def test_stage_or_digest_changes_are_rejected(self):
        a=self.approval();a['stages'][-1][-1]=60
        with self.assertRaises(ValueError):validate(a,now=100)
        a=self.approval();a['compose_sha256']='latest'
        with self.assertRaises(ValueError):validate(a,now=100)
    def test_parent_ceiling_equals_children_and_approved_db_two_cpu(self):
        values=list(slices('cohost-'+'a'*16).values())
        self.assertEqual(values[0],(300,5905580032))
        self.assertEqual(sum(v[1] for v in values[1:]),values[0][1])
        self.assertEqual(values[1],(100,2684354560))
        self.assertEqual(values[-1],(200,3221225472))
        self.assertEqual(sum(v[0] for v in values[1:]),values[0][0])
    def test_wrong_topology_stale_or_unready_observer_rejected(self):
        runner=SingleHostRunner(Path('.'),Path('unused'),self.approval(),Path('unused'))
        good=dict(run=runner.approval['run'],topology='singlehost',ready=True,age_seconds=.1)
        runner.validate_status(good)
        for key,value in (('run','another'),('topology','cohost'),('ready',False),('closed',True),('guard_error',True),('age_seconds',5.01),('age_seconds',float('nan'))):
            with self.subTest(key=key),self.assertRaises(ValueError):runner.validate_status({**good,key:value})

if __name__=='__main__':unittest.main()
