// A read-only ramp gate. It never starts Docker, provisions resources or sends
// traffic. It produces the next permitted stage after checking prior evidence.
import fs from 'node:fs';
import {RAMP} from './policy.mjs';
export function nextStage(summary,reconciliation){
 const previous=summary.configuration?.target;
 if(!summary.run_id||summary.run_id!==reconciliation.run_id||reconciliation.write_reconciliation_passed!==true)throw Error('Matching successful reconciliation required');
 if(summary.configuration?.profile==='burst')throw Error('Burst results cannot authorize a sustainable session stage');
 if(summary.configuration?.p95_ms!==500||summary.configuration?.p99_ms!==1500)throw Error('Original latency gates must be preserved');
 const metrics=summary.metrics||{};
 if(!Number.isInteger(previous)||previous<2)throw Error('Valid planned concurrency required');
 for(const name of ['successful_latency','unexpected_failure','controlled_overload','content_valid']){
  const keys=name==='successful_latency'?['p(95)','p(99)']:['rate'];
  for(const key of keys)if(!Number.isFinite(metrics[name]?.values?.[key]))throw Error('Missing finite evidence for '+name);
 }
 if((metrics.session_starts?.values.count||0)<previous||(metrics.vus?.values.max||0)<previous)throw Error('Planned concurrent sessions were not achieved');
 if((metrics.dropped_iterations?.values.count||0)>0)throw Error('Generator dropped arrivals');
 for(const metric of Object.values(metrics))for(const threshold of Object.values(metric.thresholds||{}))if(threshold.ok!==true)throw Error('A previous threshold failed');
 if(!metrics.successful_latency||metrics.successful_latency.values['p(95)']>=500||metrics.successful_latency.values['p(99)']>=1500)throw Error('Successful latency gate failed');
 if(metrics.unexpected_failure?.values.rate>=0.01||metrics.controlled_overload?.values.rate!==0||metrics.content_valid?.values.rate!==1)throw Error('Previous stage was unhealthy');
 const next=previous<100?100:RAMP[RAMP.indexOf(previous)+1];
 if(previous>=100&&!RAMP.includes(previous))throw Error('Previous concurrency is outside the reviewed sequence');
 return {previous_sessions:previous,next_sessions:next||null,resource_review_required:true,spending_approval_required:true,note:next?'Previous software gates passed. Review resource/worker/media evidence and cost before execution.':'12000 session step completed; review sustained/soak/fault/media evidence before a launch decision.'};
}
if(process.argv[1]?.endsWith('advance.mjs')){
 try{if(process.argv.length!==4)throw Error('Usage: node advance.mjs summary.json reconciliation.jsonl');const summary=JSON.parse(fs.readFileSync(process.argv[2],'utf8'));const rows=fs.readFileSync(process.argv[3],'utf8').trim().split('\n').map(line=>JSON.parse(line));const reconciled=rows.find(row=>'write_reconciliation_passed'in row);console.log(JSON.stringify(nextStage(summary,reconciled),null,2));}catch(error){console.error('STOPPED: '+error.message);process.exitCode=1;}
}
