import http from 'k6/http';
import {sleep,check} from 'k6';
import exec from 'k6/execution';
import {Counter,Trend,Rate} from 'k6/metrics';
import {randomBytes} from 'k6/crypto';
import {SharedArray} from 'k6/data';
import {plan,mixes,validateFixture,requireApproval,chooseActivity} from './policy.mjs';

const cfg=plan(__ENV.LAB_SESSION_PROFILE||'smoke',Number(__ENV.LAB_SESSION_TARGET||4));
const fixture=JSON.parse(open('/exchange/run.json'));
if(fixture.schema!==1||fixture.marker!=='disposable-compose-only'||fixture.origin!=='http://gateway:8080')throw Error('Isolated metadata required');
requireApproval(cfg,__ENV.LAB_SESSION_APPROVAL?JSON.parse(open(__ENV.LAB_SESSION_APPROVAL)):null);
const mix=mixes[__ENV.LAB_SESSION_MIX||'mixed'];
if(!mix)throw Error('Upload-heavy is blocked until isolated object-storage/processing transport is implemented.');
const sessions=new SharedArray('synthetic sessions',()=>{const full=JSON.parse(open('/exchange/fixtures.json'));validateFixture(full,cfg);if(full.run_id!==fixture.run_id)throw Error('Fixture run mismatch');return full.sessions;});
function randomUUID(){const b=new Uint8Array(randomBytes(16));b[6]=(b[6]&15)|64;b[8]=(b[8]&63)|128;const h=Array.from(b,value=>value.toString(16).padStart(2,'0')).join('');return h.slice(0,8)+'-'+h.slice(8,12)+'-'+h.slice(12,16)+'-'+h.slice(16,20)+'-'+h.slice(20);}
// Tokens are synthetic and are never logged. Pin semantic names and exclude URL
// and error tags so signed URLs/private values cannot enter raw artifacts.
const offered=new Counter('offered_requests');
const confirmed=new Counter('confirmed_writes'),unexpected=new Rate('unexpected_failure'),overload=new Rate('controlled_overload'),valid=new Rate('content_valid'),success=new Trend('successful_latency',true),started=new Counter('session_starts'),actions=new Counter('session_actions'),sent=new Counter('messages_confirmed'),received=new Counter('recipient_observations'),delivery=new Trend('poll_delivery_latency',true),backends=new Counter('backend_responses');
const perOperation=['login','session','catalogue','services','product_detail','service_detail','market_history','market_send','hall_history','hall_send','hall_offer','support_history','admin_overview','receipts','inbox'];
const threshold={unexpected_failure:[{threshold:'rate<0.005',abortOnFail:true,delayAbortEval:'10s'}],content_valid:[{threshold:'rate==1',abortOnFail:true,delayAbortEval:'10s'}],controlled_overload:cfg.profile==='burst'?['rate<=1']:[{threshold:'rate==0',abortOnFail:true,delayAbortEval:'10s'}],successful_latency:[{threshold:'p(95)<500',abortOnFail:true,delayAbortEval:'15s'},'p(99)<1500'],confirmed_writes:['count>0']};
threshold['backend_responses{backend:api1}']=['count>0'];threshold['backend_responses{backend:api2}']=['count>0'];
if(cfg.executor!=='constant-arrival-rate'){
 threshold.recipient_observations=['count>0'];
 threshold.poll_delivery_latency=[`p(95)<${cfg.poll_p95_ms}`,`p(99)<${cfg.poll_p99_ms}`];
}
for(const op of perOperation)threshold[`successful_latency{op:${op}}`]=['p(95)<500','p(99)<1500'];
if(cfg.executor!=='constant-arrival-rate')threshold.session_starts=[`count>=${cfg.target}`];else threshold.dropped_iterations=['count==0'];
export const options={scenarios:{sessions:cfg.executor==='ramping-vus'?{executor:'ramping-vus',startVUs:0,stages:[{duration:`${cfg.ramp_seconds}s`,target:cfg.target},{duration:`${cfg.seconds-cfg.ramp_seconds}s`,target:cfg.target}],gracefulStop:'15s'}:cfg.executor==='constant-vus'?{executor:'constant-vus',vus:cfg.target,duration:`${cfg.seconds}s`,gracefulStop:'15s'}:{executor:'constant-arrival-rate',rate:cfg.rate,timeUnit:'1s',duration:`${cfg.seconds}s`,preAllocatedVUs:Math.min(cfg.target,100),maxVUs:cfg.target,gracefulStop:'15s'}},thresholds:threshold,summaryTrendStats:['min','med','p(95)','p(99)','max'],systemTags:['status','method','name','scenario','expected_response'],maxRedirects:0,discardResponseBodies:false};
let actor=null,loggedIn=false,cycle=0,nextPollAt=0;const observed=new Set();
// An active conversation continues polling while its user thinks or browses.
// Do not wait for the random activity selector to choose chat again. Missed
// intervals produce one current poll, never a catch-up burst.
function waitWithPolling(seconds){
 const until=Date.now()+seconds*1000;
 while(Date.now()<until){
  if(Date.now()>=nextPollAt){history();nextPollAt=Date.now()+cfg.poll_every_ms;}
  sleep(Math.max(0.001,Math.min(until-Date.now(),nextPollAt-Date.now())/1000));
 }
}
function req(op,path,token,body,want=200,method=body?'POST':'GET',expectBody=true){
 const params={headers:{'Content-Type':'application/json','X-Forwarded-For':`10.${Math.floor((__VU-1)/65025)%250}.${Math.floor((__VU-1)/255)%255}.${(__VU-1)%255+1}`},tags:{name:op,op},redirects:0,timeout:'12s'};
 params.responseCallback=http.expectedStatuses(want);
 if(token)params.headers.Authorization=`Bearer ${token}`;
 offered.add(1,{op});
 const response=http.request(method,fixture.origin+'/api/v1'+path,body?JSON.stringify(body):null,params);
 if(response.headers['X-Lab-Backend'])backends.add(1,{backend:response.headers['X-Lab-Backend']});
 let data=null;try{if(response.body)data=response.json();}catch{}
 const busy=[429,503].includes(response.status)&&!!data&&(typeof data.error==='string'||typeof data.message==='string')&&Number(response.headers['Retry-After'])>0;
 const passed=response.status===want&&(!expectBody||want>=400||want===204||!!data);
 unexpected.add(!passed&&!busy,{op});overload.add(busy,{op});valid.add(passed||busy,{op});
 if(passed&&want<400)success.add(response.timings.duration,{op});
 check(response,{[op+' expected status and JSON']:()=>passed||busy});
 return {response,data,passed};
}
function body(){return {message:`sessions-${fixture.run_id}:${Date.now()}:${actor.id}:${randomUUID()}`,client_message_id:randomUUID()};}
function history(){
 const path=actor.role==='buyer'?'/marketplace/conversations/':'/providers/me/conversations/';
 const result=req('market_history',path+actor.conversation+'/messages?limit=40&mark_read=false',actor.token);
 if(!result.passed)return;
 const items=result.data.items;
 valid.add(Array.isArray(items)&&items.length>0&&items.length<=40,{op:'history_shape'});
 const inbound=[];const ids=new Set();let previous=Infinity;
 for(const message of items||[]){const stamp=Date.parse(message.created_at);valid.add(Number.isFinite(stamp)&&stamp<=previous&&!ids.has(message.id),{op:'history_order'});previous=stamp;ids.add(message.id);}
 for(const message of items||[]){
  if(message.sender_type===actor.role||!String(message.body).startsWith('sessions-'+fixture.run_id+':')||observed.has(message.id))continue;
  const marker=String(message.body).split(':');const intended=marker[2]===actor.peer_id;
  valid.add(intended,{op:'recipient_identity'});
  if(intended){observed.add(message.id);received.add(1);delivery.add(Math.max(0,Date.now()-Number(marker[1])));inbound.push(message.id);}
 }
 if(inbound.length)req('receipts','/messaging/receipts',actor.token,{channel:'business',conversation_id:actor.conversation,message_ids:inbound,read:true},204);
}
export function setup(){
 const ready=http.get(fixture.origin+'/api/v1/ready',{redirects:0,timeout:'10s',tags:{name:'preflight_ready'}});
 if(ready.status!==200||ready.headers['X-Atl-Capacity-Lab']!==fixture.marker)exec.test.abort('Isolated target marker/readiness missing; no workload launched');
 const first=sessions[0],peer=sessions[2];
 const who=req('session','/auth/session',first.token);if(!who.passed||who.data.user_id!==first.id)exec.test.abort('Synthetic identity does not match target');
 const expired=req('expired_session','/auth/session',fixture.expired_token,null,401);if(!expired.passed)exec.test.abort('Expired session did not return the required 401');
 const privateCheck=req('outsider_history','/marketplace/conversations/'+first.conversation+'/messages?limit=40&mark_read=false',peer.token,null,404);
 if(!privateCheck.passed)exec.test.abort('Conversation outsider authorization failed');
 return {run_id:fixture.run_id};
}
export default function(){
 if(!actor){actor={...sessions[exec.vu.idInTest-1]};if(!actor.id)exec.test.abort('Generator requested more VUs than distinct identities');started.add(1);const login=req('login','/auth/login',null,{email:actor.email,password:fixture.password});if(!login.passed||login.data.user_id!==actor.id||!login.data.access_token)exec.test.abort('Synthetic login failed');actor.token=login.data.access_token;const session=req('session','/auth/session',actor.token);if(!session.passed||session.data.user_id!==actor.id)exec.test.abort('Session binding failed');loggedIn=true;sleep(Math.random()*3);}
 if(!loggedIn)return;
 if(!nextPollAt||Date.now()>=nextPollAt){history();nextPollAt=Date.now()+cfg.poll_every_ms;}
 actions.add(1);cycle++;
 const activity=cycle===1?'chat':chooseActivity(mix,Math.random()*100);
 if(activity==='browse'){
  const term=['Synthetic','hotel','trousers'][Math.floor(Math.random()*3)];
  req('catalogue','/products?limit=20&search='+term+(cycle%7===0?'&stock_scope=local':''));
  req('services','/marketplace/listings?limit=20&search='+term);
  if(cycle%2===0)req('product_detail','/products/'+actor.product);else req('service_detail','/marketplace/listings/'+actor.listing);
 }else if(activity==='chat'){
  history();const path=actor.role==='buyer'?'/marketplace/conversations/':'/providers/me/conversations/';const message=body(),saved=req('market_send',path+actor.conversation+'/messages',actor.token,message,201);
  if(saved.passed){console.log('ACK:'+message.message);confirmed.add(1);sent.add(1);if(cycle===1){const replay=req('market_retry',path+actor.conversation+'/messages',actor.token,message,201);valid.add(replay.passed&&replay.data.id===saved.data.id,{op:'idempotent_message'});}}
  req('inbox','/chat/inbox?limit=25',actor.token);
 }else if(activity==='hall'){
  const room=actor.role==='provider'&&actor.hot_room?actor.hot_room:actor.room;
  req('hall_history','/chat/conversations/'+room+'/messages?limit=40&mark_read=false',actor.token);
  const message=body();const saved=req('hall_send','/chat/conversations/'+room+'/messages',actor.token,message,201);if(saved.passed){console.log('ACK:'+message.message);confirmed.add(1);}
 }else if(activity==='support'){
  if(actor.role==='buyer')req('support_history','/support/tickets/'+actor.ticket+'/messages?limit=40',actor.token);else req('session','/providers/me',actor.token);
 }else if(activity==='admin')req('admin_overview','/admin/business-overview',fixture.admin_token);
 if(cfg.reconnect&&cycle%3===0){sleep(5+Math.random()*15);req('session','/auth/session',actor.token);nextPollAt=Date.now();}
 waitWithPolling(cfg.think_min+Math.random()*(cfg.think_max-cfg.think_min));
}
export function handleSummary(data){
 const evidence={...data,run_id:fixture.run_id,configuration:cfg,mix,limitations:['internal Docker network','generator shares CI host','all sessions keep an active conversation polling at 15s','no real media or full worker process drain in this workload','signup/login spikes not qualified','VU session counts differ from requests in flight','no native rendering qualification']};
 return {'/exchange/summary.json':JSON.stringify(evidence,null,2),stdout:JSON.stringify({run_id:fixture.run_id,profile:cfg.profile,planned_concurrent_sessions:cfg.target,session_starts:data.metrics.session_starts?.values.count,actions:data.metrics.session_actions?.values.count,confirmed_writes:data.metrics.confirmed_writes?.values.count,recipient_observations:data.metrics.recipient_observations?.values.count,scope:'isolated HTTP/polling sessions; not phone delivery'})+'\n'};
}
