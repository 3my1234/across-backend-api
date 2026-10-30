import assert from 'node:assert/strict';
import fs from 'node:fs';import os from 'node:os';import path from 'node:path';import {spawnSync} from 'node:child_process';
import {plan} from '../session-lab/policy.mjs';
const temp=fs.mkdtempSync(path.join(os.tmpdir(),'cohost-stage-'));
try{
 const model=plan('sustained',500);
 const metrics={successful_latency:{values:{'p(95)':80,'p(99)':100}},unexpected_failure:{values:{rate:0}},controlled_overload:{values:{rate:0}},content_valid:{values:{rate:1}},session_starts:{values:{count:500}},vus:{values:{max:500}},poll_delivery_latency:{values:{'p(95)':100,'p(99)':200}},recipient_observations:{values:{count:10}}};
 const original={run_id:'synthetic',configuration:model,metrics,authentication_mode:'established',expanded_journeys:true};
 const reconcile={run_id:'synthetic',write_reconciliation_passed:true};
 const summary=path.join(temp,'summary.json'),reconciliation=path.join(temp,'reconciliation.jsonl');
 function run(s,r=reconcile){fs.writeFileSync(summary,JSON.stringify(s));fs.writeFileSync(reconciliation,JSON.stringify(r)+'\n');return spawnSync(process.execPath,['deploy/cohost-lab/stage_gate.mjs',summary,reconciliation,s.configuration.profile,'500'],{encoding:'utf8'});}
 assert.equal(run(original).status,0);
 for(const alter of [s=>s.configuration.p95_ms=600,s=>s.metrics.successful_latency.values['p(95)']=501,s=>s.metrics.controlled_overload.values.rate=.01,s=>s.metrics.content_valid.values.rate=.99,s=>s.metrics.vus.values.max=499,s=>s.metrics.dropped_iterations={values:{count:1}},s=>s.expanded_journeys=false,s=>s.authentication_mode='live']){
  const s=structuredClone(original);alter(s);assert.notEqual(run(s).status,0);
 }
 assert.notEqual(run(original,{...reconcile,write_reconciliation_passed:false}).status,0);
 const gradual=structuredClone(original);gradual.configuration=plan('gradual',500);
 const result=run(gradual);assert.equal(result.status,0);assert.equal(JSON.parse(result.stdout).capacity_qualified,false);
 console.log('Cohost stage gates reject changed models, errors, missing concurrency, dropped traffic and failed integrity.');
}finally{fs.rmSync(temp,{recursive:true,force:true});}
