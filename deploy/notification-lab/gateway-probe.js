import http from 'k6/http';
import { check } from 'k6';
export const options={scenarios:{burst:{executor:'per-vu-iterations',vus:500,iterations:1,maxDuration:'30s'}},thresholds:{http_req_failed:['rate==0'],checks:['rate==1']}};
export default function(){
 const r=http.get(`${__ENV.TARGET}/api/v1/health`);
 check(r,{'synthetic health response':x=>x.status===200&&x.json('synthetic_only')===true});
}
