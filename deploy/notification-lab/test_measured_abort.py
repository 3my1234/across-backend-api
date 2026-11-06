import json,subprocess,unittest
from pathlib import Path
from measured_abort import PATCH,candidate,recover

class MeasuredAbortTests(unittest.TestCase):
    def test_candidate_reversible_and_original_traffic_unchanged(self):
        source=(Path(__file__).parents[1]/'session-lab/sessions.js').read_text()
        result=candidate(source)
        self.assertEqual(recover(result),source)
        self.assertIn("success.add(response.timings.duration,{op})",result)
        with self.assertRaises(ValueError):candidate(result)

    def test_actual_patch_buffers_without_discarding_early_samples(self):
        program="""
let now=100000;let samples=[];
const exec={scenario:{startTime:100000}};
Date.now=()=>now;
class Trend {add(value){samples.push(value)}}
const options={thresholds:{}};
"""+PATCH+"""
recordMeasuredAbort(600);now+=14999;recordMeasuredAbort(700);
if(samples.length)throw Error('aborted before measured grace');
now+=1;recordMeasuredAbort(10);recordMeasuredAbort(20);
if(JSON.stringify(samples)!=='[600,700,10,20]')throw Error('early samples discarded');
if(JSON.stringify(options.thresholds.successful_latency)!=='["p(95)<500","p(99)<1500"]')throw Error('final gates changed');
console.log(JSON.stringify({samples,thresholds:options.thresholds}));
"""
        result=subprocess.run(['node','-e',program],capture_output=True,text=True,check=True,timeout=10)
        self.assertEqual(json.loads(result.stdout)['samples'],[600,700,10,20])
