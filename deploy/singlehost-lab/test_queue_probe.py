import unittest
from queue_probe import exercise

class ProbeTests(unittest.TestCase):
    def test_existing_workload_jobs_are_never_drained_or_deleted(self):
        calls=[]
        def sql(text):calls.append(text);return 'disposable-compose-only' if text.startswith('SELECT marker') else '1'
        with self.assertRaisesRegex(ValueError,'empty pre-workload'):exercise(sql,lambda:self.fail('must not run worker'))
        self.assertFalse(any('DELETE' in text or 'INSERT' in text for text in calls))
    def test_failed_worker_check_still_deletes_only_identified_probe(self):
        calls=[];counts=iter(['0','1','0'])
        def sql(text):
            calls.append(text)
            if text.startswith('SELECT marker'):return 'disposable-compose-only'
            if text.startswith('SELECT count'):return next(counts)
            if text.startswith('INSERT'):return 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\nINSERT 0 1'
            if text.startswith('WITH'):return '1'
            raise AssertionError(text)
        def worker():raise RuntimeError('worker test failure')
        with self.assertRaisesRegex(RuntimeError,'worker test failure'):exercise(sql,worker)
        cleanup=next(text for text in calls if text.startswith('WITH'))
        self.assertIn("id='aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'::uuid AND event_key='singlehost-queue-probe:",cleanup)
        self.assertIn("source_key='notification:aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'",cleanup)

if __name__=='__main__':unittest.main()
