import json,unittest
from unittest.mock import Mock,patch
import watchdog as w

class NativeState(unittest.TestCase):
    def connection(self,rows,status=200):
        connection=Mock();connection.sock=None
        connection.getresponse.side_effect=[Mock(status=status,read=Mock(return_value=json.dumps(row).encode())) for row in rows]
        return connection
    def test_same_running_health_and_oom_state_for_every_exact_id(self):
        ids=['a'*64,'b'*64];states=[dict(Running=True,OOMKilled=False,Health=dict(Status='healthy')),dict(Running=False,OOMKilled=True)]
        connection=self.connection([dict(Id=i,State=s) for i,s in zip(ids,states)])
        with patch.object(w,'docker_connection',return_value=connection):self.assertEqual(w.native_state(ids),states)
        self.assertEqual([c.args for c in connection.request.call_args_list],[('GET','/containers/'+i+'/json') for i in ids]);connection.close.assert_called_once()
    def test_missing_or_replaced_container_never_reports_healthy(self):
        for row,status in ((dict(Id='b'*64,State=dict(Running=True)),200),(dict(message='missing'),404)):
            connection=self.connection([row],status)
            with patch.object(w,'docker_connection',return_value=connection),self.assertRaises(ValueError):w.native_state(['a'*64])
            connection.close.assert_called_once()
    def test_batch_retains_three_second_total_deadline(self):
        connection=self.connection([dict(Id='a'*64,State={})])
        with patch.object(w,'docker_connection',return_value=connection),patch.object(w.time,'monotonic',side_effect=[0,3.01]),self.assertRaises(TimeoutError):w.native_state(['a'*64])
        connection.request.assert_not_called();connection.close.assert_called_once()
    def test_paths_cannot_be_injected(self):
        with patch.object(w,'docker_connection') as connect,self.assertRaises(ValueError):w.native_state(['../production'])
        connect.assert_not_called()

if __name__=='__main__':unittest.main()
