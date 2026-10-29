// Offline renderer only. JSON output is a valid Compose document; never executes Docker.
const fs = require('fs');
const path = require('path');
let YAML;
try { YAML = require('yaml'); } catch { YAML = require(path.resolve(__dirname, '../../../../mobile/node_modules/yaml')); }
const root = path.resolve(__dirname, '../..');
function render(input) {
  if (!/^cohost-[a-f0-9]{16}$/.test(input.run)) throw Error('unique run nonce required');
  if (!['main','database'].includes(input.role)) throw Error('invalid role');
  for (const name of ['app','postgres','redis','gateway','socat']) {
    if (!/^(?:[a-zA-Z0-9./:_-]+@)?sha256:[a-f0-9]{64}$/.test(input.images[name] || '')) throw Error('locked digest required: '+name);
  }
  const base = '/root/atlxpres-cohost/'+input.run;
  const label = {'atlxpres.cohost.run':input.run};
  const parent = input.run.replaceAll('-','')+input.role+'.slice';
  if (input.role === 'database') return {
    name:input.run+'-database',
    services:{postgres:{image:input.images.postgres,
      command:['postgres','-c','cluster_name='+input.run,'-c','shared_preload_libraries=pg_stat_statements','-c','log_parameter_max_length=0','-c','log_parameter_max_length_on_error=0'],
      environment:{POSTGRES_USER:'lab',POSTGRES_PASSWORD:'lab-disposable-only',POSTGRES_DB:'capacity_lab'},
      cpus:1,mem_limit:'3g',memswap_limit:'3g',pids_limit:256,cgroup_parent:parent,labels:label,
      restart:'no',ports:['127.0.0.1:55432:5432'],volumes:['synthetic-pg:/var/lib/postgresql/data'],
      healthcheck:{test:['CMD-SHELL','pg_isready -U lab -d capacity_lab'],interval:'2s',timeout:'2s',retries:30}}},
    volumes:{'synthetic-pg':{labels:label}},networks:{default:{internal:true,labels:label}}};
  const paths=['capacity-lab','session-lab','notification-lab'];
  const services={};
  for (const folder of paths) {
    const source=YAML.parse(fs.readFileSync(path.join(root,'deploy',folder,'compose.yml'),'utf8'));
    for(const [name,value] of Object.entries(source.services)) {
      const previous=services[name] || {};
      services[name]={...previous,...value,environment:{...previous.environment,...value.environment}};
    }
  }
  delete services.postgres; delete services.sessions; delete services.runner;
  for(const [name,s] of Object.entries(services)) {
    delete s.build; delete s.depends_on;
    s.image = name === 'redis' ? input.images.redis : name === 'gateway' ? input.images.gateway : input.images.app;
    if(['seed','exporter','verifier','queue-check','lifecycle'].includes(name)) s.command=['/bin/sleep','infinity'];
    if(['api1','api2'].includes(name)) s.environment.DATABASE_URL += '&application_name=system-'+name;
    s.labels=label;s.cgroup_parent=parent;s.restart='no';s.memswap_limit=s.mem_limit;s.pids_limit=256;
    if(s.volumes) s.volumes=s.volumes.map(v=>v.includes(':/exchange') ? base+'/exchange:/exchange'+(v.endsWith(':ro')?':ro':'') : v);
    if(name==='gateway') {
      s.cpus=1;s.ports=['127.0.0.1:58080:8080'];
      s.volumes=[base+'/haproxy.cfg:/usr/local/etc/haproxy/haproxy.cfg:ro'];
    }
    if(s.environment) {
      for(const [key,value] of Object.entries(s.environment)) if(typeof value==='string' && value.includes('${')) {
        const choices={LAB_SCENARIO:'large-busy',LAB_ACTOR_PAIRS:'500',LAB_NOTIFICATION_WORKERS:'1',LAB_DB_CPUS:'3',LAB_EXPANDED_JOURNEYS:'1'};
        if(!(key in choices)) throw Error('unresolved setting '+key);
        s.environment[key]=choices[key];
      }
    }
  }
  services['db-probe']={image:input.images.postgres,command:['/bin/sleep','infinity'],user:'0:0',
    environment:{PGPASSWORD:'lab-disposable-only'},volumes:[base+'/tunnel:/tunnel:ro'],networks:['lab'],
    labels:label,cgroup_parent:parent,cpus:.25,mem_limit:'128m',memswap_limit:'128m',pids_limit:64,restart:'no'};
  services.postgres={image:input.images.socat,user:'0:0',command:['TCP-LISTEN:5432,fork,reuseaddr','UNIX-CONNECT:/tunnel/.s.PGSQL.55432'],
    volumes:[base+'/tunnel:/tunnel:ro'],networks:['lab'],labels:label,cgroup_parent:parent,
    cpus:.25,mem_limit:'64m',memswap_limit:'64m',pids_limit:128,restart:'no'};
  return {name:input.run+'-main',services,networks:{lab:{internal:true,labels:label,ipam:{config:[{subnet:'172.29.248.0/24'}]}}}};
}
if(require.main===module) console.log(JSON.stringify(render(JSON.parse(fs.readFileSync(process.argv[2],'utf8'))),null,2));
module.exports={render};
