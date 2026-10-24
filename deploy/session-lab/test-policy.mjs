import assert from 'node:assert/strict';
import {plan,validateFixture,requireApproval,RAMP,chooseActivity,mixes} from './policy.mjs';
import {nextStage} from './advance.mjs';
assert.deepEqual(RAMP,[100,500,1000,2500,5000,10000,12000]);
for(const n of [0,1,12001.5,15001,NaN])assert.throws(()=>plan('stage',n));
assert.throws(()=>plan('smoke',100));assert.throws(()=>plan('unknown',4));
const fresh={schema:1,marker:'disposable-compose-only',origin:'http://gateway:8080',run_id:'00000000-0000-4000-8000-000000000001',expires_at:new Date(Date.now()+3600000).toISOString(),sessions:Array.from({length:4},(_,i)=>({id:'user-'+i,role:i%2?'provider':'buyer',email:i+'@example.invalid'}))};
validateFixture(fresh,plan());
for(const origin of ['https://api.atlxpres.com','https://staging.atlxpres.com','http://173.212.219.169','http://127.0.0.1:8080'])assert.throws(()=>validateFixture({...fresh,origin},plan()));
assert.throws(()=>validateFixture({...fresh,sessions:[fresh.sessions[0],fresh.sessions[0],...fresh.sessions.slice(2)]},plan()));
assert.throws(()=>validateFixture({...fresh,expires_at:'2000-01-01'},plan()));
assert.throws(()=>validateFixture({...fresh,expires_at:'invalid'},plan()));
assert.throws(()=>validateFixture(fresh,plan('stage',12000)));
const stage=plan('sustained',12000);assert.throws(()=>requireApproval(stage));
const approval={authorized:true,environment:'isolated-staging',profile:'sustained',max_sessions:12000,max_duration_seconds:1800,spending_cap_usd:5,approved_at:new Date().toISOString()};requireApproval(stage,approval);
for(const change of [{authorized:false},{max_sessions:100},{max_duration_seconds:600},{spending_cap_usd:-1},{approved_at:'bad'},{approved_at:'2000-01-01'},{profile:'soak'}])assert.throws(()=>requireApproval(stage,{...approval,...change}));
for(const key of ['max_sessions','max_duration_seconds'])for(const value of [undefined,null,'12000',NaN,Infinity])assert.throws(()=>requireApproval(stage,{...approval,[key]:value}));
requireApproval(plan(),null);
assert.equal(chooseActivity(mixes.mixed,0),'browse');assert.equal(chooseActivity(mixes.mixed,99),'admin');
for(const name of ['mixed','chat-heavy'])assert.equal(Object.values(mixes[name]).reduce((a,b)=>a+b,0),100);
console.log('Session policy passed: explicit isolated target, unique identities, expiry, bounded smoke, 12000 stage plan and cost-approval gates.');
const evidence={run_id:'test',configuration:plan(),metrics:{session_starts:{values:{count:4}},vus:{values:{max:4}},successful_latency:{values:{'p(95)':10,'p(99)':20},thresholds:{latency:{ok:true}}},unexpected_failure:{values:{rate:0}},controlled_overload:{values:{rate:0}},content_valid:{values:{rate:1}}}};
evidence.metrics.poll_delivery_latency={values:{'p(95)':14000,'p(99)':15000}};
evidence.metrics.recipient_observations={values:{count:3}};
const recon={run_id:'test',write_reconciliation_passed:true};assert.equal(nextStage(evidence,recon).next_sessions,100);
for(const [index,target] of RAMP.entries()){
 const staged={...evidence,configuration:plan('stage',target),metrics:{...evidence.metrics,session_starts:{values:{count:target}},vus:{values:{max:target}}}};
 assert.equal(nextStage(staged,recon).next_sessions,RAMP[index+1]||null);
}
for(const change of [{poll_every_ms:undefined},{seconds:1},{profile:'unknown'},{think_min:0}])assert.throws(()=>nextStage({...evidence,configuration:{...evidence.configuration,...change}},recon));
for(const name of ['session_starts','vus']){const metrics={...evidence.metrics};delete metrics[name];assert.throws(()=>nextStage({...evidence,metrics},recon));}
assert.throws(()=>nextStage({...evidence,metrics:{...evidence.metrics,poll_delivery_latency:{values:{'p(95)':18000,'p(99)':19000}}}},recon));
assert.throws(()=>nextStage({...evidence,metrics:{...evidence.metrics,recipient_observations:{values:{count:0}}}},recon));
assert.throws(()=>nextStage(evidence,{...recon,run_id:'other'}));assert.throws(()=>nextStage({...evidence,metrics:{...evidence.metrics,controlled_overload:{values:{rate:0.1}}}},recon));assert.throws(()=>nextStage({...evidence,metrics:{...evidence.metrics,vus:{values:{max:3}}}},recon));assert.throws(()=>nextStage({...evidence,metrics:{...evidence.metrics,successful_latency:{values:{'p(95)':700,'p(99)':900}}}},recon));
console.log('Ramp gate passed: matched evidence, achieved concurrency and unchanged health/latency criteria before next-stage review.');
for(const name of ['unexpected_failure','successful_latency','content_valid','controlled_overload']){
 const metrics={...evidence.metrics};delete metrics[name];assert.throws(()=>nextStage({...evidence,metrics},recon));
}

assert.equal(plan('gradual',100).ramp_seconds,30);assert.equal(plan('gradual',100).seconds,180);assert.equal(plan('gradual',100).p95_ms,500);
