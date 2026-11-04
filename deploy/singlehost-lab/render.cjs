// Render only. No SSH, Docker invocation, or production configuration reads.
const {render:cohost} = require('../cohost-lab/render.cjs');
const fs = require('fs');
function render(input) {
  if (!/^cohost-[a-f0-9]{16}$/.test(input.run)) throw Error('fixed unique nonce required');
  for (const name of ['app','postgres','redis','gateway'])
    if (!/^sha256:[a-f0-9]{64}$/.test(input.images[name] || '')) throw Error('immutable image required: '+name);
  const run=input.run, base='/root/atlxpres-singlehost/'+run;
  const parent=run.replaceAll('-','')+'single';
  const doc=cohost({...input,role:'main',images:{...input.images,socat:input.images.postgres}});
  const db=cohost({...input,role:'database',images:{...input.images,socat:input.images.postgres}}).services.postgres;
  delete doc.services['db-probe'];
  doc.services.postgres=db;
  // The image's temporary initialization server listens on Unix sockets only.
  // Remote seed/API clients require the final TCP listener, not pg_isready's default socket.
  doc.services.postgres.healthcheck.test=['CMD-SHELL','pg_isready -h 127.0.0.1 -U lab -d capacity_lab'];
  doc.name=run+'-singlehost';
  doc.volumes={'synthetic-pg':{labels:{'atlxpres.cohost.run':run}}};
  for (const [name,s] of Object.entries(doc.services)) {
    s.cgroup_parent=parent+(name==='postgres'?'-db.slice':'-app.slice');
    s.networks=name==='gateway'?{lab:{ipv4_address:'172.29.248.10'}}:['lab'];
    s.labels['atlxpres.singlehost']='true';
    if (s.volumes) s.volumes=s.volumes.map(v=>v.replace('/root/atlxpres-cohost/'+run,base));
    if (s.environment && Object.hasOwn(s.environment,'LAB_DB_CPUS')) s.environment.LAB_DB_CPUS='1';
  }
  // The host can reach its own private bridge. SSH forwards ONLY this exact
  // gateway address; no Docker published-port/NAT or host relay is needed.
  delete doc.services.gateway.ports;
  // Docker may assign a stopped static endpoint's address to a dynamic service
  // started first. Keep all dynamic allocations outside the reserved low addresses.
  doc.networks.lab.ipam.config[0].ip_range='172.29.248.128/25';
  return doc;
}
if (require.main===module) console.log(JSON.stringify(render(JSON.parse(fs.readFileSync(process.argv[2],'utf8'))),null,2));
module.exports={render};
