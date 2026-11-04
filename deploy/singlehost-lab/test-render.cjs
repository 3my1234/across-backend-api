const assert=require('assert/strict');
const {render}=require('./render.cjs');
const images=Object.fromEntries(['app','postgres','redis','gateway'].map((n,i)=>[n,'sha256:'+String(i+1).repeat(64)]));
const run='cohost-'+'a'.repeat(16),doc=render({run,images});
assert.equal(doc.name,run+'-singlehost');
assert.equal(doc.services.postgres.image,images.postgres);
assert.equal(doc.services.postgres.cpus,1);
assert.equal(doc.services.postgres.mem_limit,'3g');
assert.equal(doc.services['db-probe'],undefined);
assert.deepEqual(doc.services.gateway.ports,['127.0.0.1:58080:8080']);
assert.equal(doc.networks.lab.internal,true);
for(const [name,s] of Object.entries(doc.services)) {
  assert(!s.build && !s.privileged && !s.network_mode && !s.depends_on);
  assert.equal(s.labels['atlxpres.cohost.run'],run);
  assert.equal(s.cgroup_parent,'cohost'+ 'a'.repeat(16)+'single'+(name==='postgres'?'-db.slice':'-app.slice'));
  for(const v of s.volumes||[]) assert(!v.includes('/tunnel') && !v.includes('/atlxpres-cohost/'));
  if (name!=='gateway') assert.equal(s.ports,undefined);
}
assert.equal(doc.services.seed.environment.LAB_DB_CPUS,'1');
assert.equal(doc.services.api1.environment.DB_MAX_CONNS,'8');
assert.equal(doc.services.api2.environment.HTTP_MAX_IN_FLIGHT,'64');
assert(doc.services.api1.environment.DATABASE_URL.includes('@postgres:5432/capacity_lab'));
assert(!JSON.stringify(doc).includes('UNIX-CONNECT'));
assert.throws(()=>render({run:'production',images}));
assert.throws(()=>render({run,images:{...images,app:'app:latest'}}));
console.log('single-host renderer isolation/caps/identity checks passed');
