// Pure policy shared by Node safety tests and k6. No network or secrets here.
export const RAMP=[100,500,1000,2500,5000,10000,12000];
export const mixes={mixed:{browse:40,chat:30,hall:20,support:5,admin:5},'chat-heavy':{browse:15,chat:50,hall:30,support:3,admin:2},'upload-heavy':null};
export function plan(profile='smoke',target=4){
 if(!Number.isInteger(target)||target<2||target>15000)throw Error('Target must be 2–15000 distinct sessions');
 const common={profile,target,think_min:3,think_max:12,p95_ms:500,p99_ms:1500,error_rate:0.01,poll_every_ms:15000,poll_p95_ms:17000,poll_p99_ms:20000};
 switch(profile){
 case 'smoke':if(target>20)throw Error('Smoke is capped at 20 sessions');return {...common,seconds:45,executor:'constant-vus'};
 case 'stage':return {...common,seconds:180,executor:'constant-vus'};
 case 'gradual':return {...common,seconds:180,ramp_seconds:30,executor:'ramping-vus'};
 case 'sustained':return {...common,seconds:1800,executor:'constant-vus'};
 case 'soak':return {...common,seconds:7200,executor:'constant-vus'};
 case 'stress':return {...common,seconds:300,executor:'constant-vus'};
 case 'reconnect':return {...common,seconds:120,executor:'constant-vus',reconnect:true};
 case 'burst':return {...common,seconds:90,executor:'constant-arrival-rate',rate:target};
 default:throw Error('Unknown session profile');
 }
}
export function validateFixture(fixture,configuration){
 if(fixture.schema!==1||fixture.marker!=='disposable-compose-only'||fixture.origin!=='http://gateway:8080')throw Error('Only exported isolated Compose fixtures are accepted');
 if(!/^[a-f0-9-]{36}$/.test(fixture.run_id)||!Number.isFinite(Date.parse(fixture.expires_at))||Date.parse(fixture.expires_at)<=Date.now())throw Error('Invalid or expired fixture run');
 if(!Array.isArray(fixture.sessions)||fixture.sessions.length<Math.max(4,configuration.target))throw Error('Not enough distinct synthetic sessions for requested target');
 const seen=new Set();for(const item of fixture.sessions){if(seen.has(item.id)||!['buyer','provider'].includes(item.role)||!item.email.endsWith('@example.invalid'))throw Error('Invalid or duplicate synthetic identity');seen.add(item.id);}
}
export function requireApproval(configuration,approval){
 if(configuration.profile==='smoke')return;
 if(!approval||approval.authorized!==true||approval.environment!=='isolated-staging'||approval.profile!==configuration.profile||!Number.isInteger(approval.max_sessions)||approval.max_sessions<configuration.target||!Number.isInteger(approval.max_duration_seconds)||approval.max_duration_seconds<configuration.seconds||!Number.isFinite(approval.spending_cap_usd)||approval.spending_cap_usd<0||Date.now()-Date.parse(approval.approved_at)>86400000||Date.parse(approval.approved_at)>Date.now()+300000||!Number.isFinite(Date.parse(approval.approved_at)))throw Error('A current explicit resource/cost approval is required outside smoke');
}
export function chooseActivity(mix,value){let edge=0;for(const [name,weight]of Object.entries(mix)){edge+=weight;if(value<edge)return name;}throw Error('Workload weights must sum to 100');}
