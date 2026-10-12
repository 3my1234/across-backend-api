// Run only against an isolated staging deployment with synthetic accounts.
import http from 'k6/http';
import {check,sleep} from 'k6';
const origin=(__ENV.STAGING_ORIGIN || '').replace(/\/$/,'');
if(__ENV.CAPACITY_TEST_STAGING!=='yes' || !/^https?:\/\/(localhost(?=[:/]|$)|127\.0\.0\.1(?=[:/]|$)|[^/]*staging[^/]*)(:\d+)?(?:\/|$)/i.test(origin))throw Error('Use CAPACITY_TEST_STAGING=yes and a staging/loopback STAGING_ORIGIN; never run this against production.');
const accounts=JSON.parse(open(__ENV.CHAT_FIXTURES_FILE));
if(!Array.isArray(accounts)||accounts.length<100||accounts.some(a=>!a.token||!a.conversation_id))throw Error('Provide at least 100 synthetic token/conversation_id fixtures in a private JSON file.');
export const options={stages:[{duration:'30s',target:20},{duration:'1m',target:20},{duration:'30s',target:50},{duration:'1m',target:50},{duration:'30s',target:100},{duration:'2m',target:100},{duration:'15s',target:0}],thresholds:{http_req_failed:['rate<0.01'],http_req_duration:['p(95)<500'],checks:['rate>0.99']}};
let etag='';
export default function(){
 const actor=accounts[(__VU-1)%accounts.length],headers={Authorization:`Bearer ${actor.token}`};
 const inbox=http.get(`${origin}/api/v1/marketplace/conversations?limit=20`,{headers,tags:{name:'chat inbox'}});
 check(inbox,{'inbox available':r=>r.status===200,'inbox bounded':r=>r.status===200&&r.json('items').length<=20});
 if(etag)headers['If-None-Match']=etag;
 const history=http.get(`${origin}/api/v1/marketplace/conversations/${actor.conversation_id}/messages?limit=50&mark_read=false`,{headers,tags:{name:'chat history'}});
 check(history,{'history available':r=>r.status===200||r.status===304,'history bounded':r=>r.status===304||r.status===200&&r.json('items').length<=50});
 if(history.status===200)etag=history.headers.Etag || history.headers.ETag || '';
 sleep(15+Math.random()*2);
}
