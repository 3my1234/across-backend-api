// Uses original declared model and advancement gate; gradual never authorizes capacity.
import fs from 'node:fs';
import {plan} from '../session-lab/policy.mjs';
import {nextStage} from '../session-lab/advance.mjs';
const [summaryFile,reconcileFile,profile,target]=process.argv.slice(2);
const s=JSON.parse(fs.readFileSync(summaryFile));
const rows=fs.readFileSync(reconcileFile,'utf8').trim().split('\n').map(JSON.parse);
const r=rows.find(x=>'write_reconciliation_passed' in x);
if(!r||r.write_reconciliation_passed!==true||r.run_id!==s.run_id)throw Error('original reconciliation failed');
const expected=plan(profile,Number(target));
for(const [key,value] of Object.entries(expected))if(s.configuration?.[key]!==value)throw Error('original workload differs: '+key);
if(s.authentication_mode!=='established'||s.expanded_journeys!==true)throw Error('expanded established sessions required');
for(const m of Object.values(s.metrics||{}))for(const v of Object.values(m.thresholds||{}))if(v.ok!==true)throw Error('original threshold failed');
const values=s.metrics.successful_latency?.values;
if(!Number.isFinite(values?.['p(95)'])||!Number.isFinite(values?.['p(99)'])||values['p(95)']>=500||values['p(99)']>=1500)throw Error('original latency gate');
for(const [name,check] of [['controlled_overload',x=>x===0],['unexpected_failure',x=>Number.isFinite(x)&&x<.005],['content_valid',x=>x===1]])if(!check(s.metrics[name]?.values.rate))throw Error('original error/content gate');
if((s.metrics.dropped_iterations?.values.count||0)>0)throw Error('generator dropped traffic');
if(profile==='gradual'){
 // Preliminary ramp only. Retain declaration and evidence; original policy forbids treating this as a burst pass.
 if((s.metrics.vus?.values.max||0)<Number(target)||(s.metrics.session_starts?.values.count||0)<Number(target))throw Error('concurrency not achieved');
 console.log(JSON.stringify({preliminary_ramp_passed:true,capacity_qualified:false}));
}else console.log(JSON.stringify(nextStage(s,r)));
