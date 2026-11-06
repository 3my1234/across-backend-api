"""Candidate timing correction. Not enabled in the qualified runner until requalified.

Keep every sample in the original final latency gate. Delay the abort-only copy
until 15 seconds of measured scenario time, then include all buffered samples.
No HTTP requests, actor selection, payloads or final thresholds are changed.
"""
ANCHOR='let actor=null,loggedIn=false'
SAMPLE='if(passed&&want<400)success.add(response.timings.duration,{op});'
REPLACEMENT='if(passed&&want<400){success.add(response.timings.duration,{op});recordMeasuredAbort(response.timings.duration);}'
PATCH="""
const measuredAbort=new Trend('measured_latency_abort',true);
let pendingAbortSamples=[];
options.thresholds.successful_latency=['p(95)<500','p(99)<1500'];
options.thresholds.measured_latency_abort=[{threshold:'p(95)<500',abortOnFail:true,delayAbortEval:'15s'}];
function recordMeasuredAbort(value){
 if(pendingAbortSamples!==null){
  pendingAbortSamples.push(value);
  if(Date.now()-exec.scenario.startTime<15000)return;
  for(const sample of pendingAbortSamples)measuredAbort.add(sample);
  pendingAbortSamples=null;
 }else measuredAbort.add(value);
}
"""

def candidate(source):
    if source.count(ANCHOR)!=1 or source.count(SAMPLE)!=1:
        raise ValueError('Reviewed measurement anchors changed')
    return source.replace(ANCHOR,PATCH+'\n'+ANCHOR,1).replace(SAMPLE,REPLACEMENT,1)

def recover(source):
    return source.replace(PATCH+'\n','',1).replace(REPLACEMENT,SAMPLE,1)
