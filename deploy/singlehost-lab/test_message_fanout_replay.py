import copy,unittest
from pathlib import Path,PureWindowsPath,PurePosixPath
from unittest.mock import patch
from message_fanout_replay import configure,require_ledger,mode

class FanoutReplayTests(unittest.TestCase):
    def test_refuses_unapproved_mode_and_non_ci(self):
        with patch.dict('os.environ',{'MESSAGE_FANOUT_REPLAY':'other','GITHUB_ACTIONS':'true'}):
            with self.assertRaises(ValueError):mode()
        with patch.dict('os.environ',{'MESSAGE_FANOUT_REPLAY':'async','GITHUB_ACTIONS':'false'}):
            with self.assertRaises(ValueError):mode()
    def test_matching_caps_and_pool_budget_in_both_arms(self):
        original={'services':{name:dict(environment={'DATABASE_URL':'postgres://postgres?application_name=system-api1','DB_MAX_CONNS':'8'},cpus=.75,mem_limit='512m',cgroup_parent='same-app.slice',healthcheck={'test':['dummy']}) for name in ('api1','api2')}}
        for arm in ('inline','async'):
            doc=copy.deepcopy(original)
            with patch('message_fanout_replay.mode',return_value=arm):configure(doc,Path('/owned'))
            for name in ('api1','api2'):
                self.assertEqual(doc['services'][name]['environment']['DB_MAX_CONNS'],'8')
                self.assertEqual(doc['services'][name]['cgroup_parent'],'same-app.slice')
            worker=doc['services']['message-fanout']
            self.assertEqual(worker['environment']['WORKER_ROLES'],'message_fanout')
            self.assertEqual(worker['environment']['DB_MAX_CONNS'],'2')
            self.assertEqual(worker['cgroup_parent'],'same-app.slice')
    def test_missing_delivery_or_job_cannot_pass(self):
        observation={'failed':0,'pending':0,'jobs':1,'max_completed_lag_seconds':1}
        ledger={'expected_jobs':1,'expected_notifications':2,'actual_notifications':2,'expected_native':2,'missing_jobs':0,'missing_notifications':0,'payload_mismatches':0,'missing_native':0,'missing_web':0}
        require_ledger('async',observation,ledger)
        for key in ('missing_jobs','missing_notifications','payload_mismatches','missing_native','missing_web'):
            with self.assertRaises(ValueError):require_ledger('async',observation,{**ledger,key:1})
        for key,value in (('failed',1),('pending',1),('max_completed_lag_seconds',11)):
            with self.assertRaises(ValueError):require_ledger('async',{**observation,key:value},ledger)
        with self.assertRaises(ValueError):require_ledger('inline',observation,ledger)
        with self.assertRaises(ValueError):require_ledger('async',observation,{**ledger,'expected_jobs':0})

    def test_worker_mount_is_linux_absolute_from_windows_packager(self):
        for base in [PureWindowsPath('/root/atlxpres-singlehost/cohost-'+'a'*16),PurePosixPath('/root/atlxpres-singlehost/cohost-'+'a'*16)]:
            doc={'services':{name:dict(environment={'DATABASE_URL':'postgres://postgres?application_name=system-api1'}) for name in ('api1','api2')}}
            configure(doc,base,arm='async')
            self.assertEqual(doc['services']['message-fanout']['volumes'],['/root/atlxpres-singlehost/cohost-'+'a'*16+'/worker.bin:/app/worker:ro'])
