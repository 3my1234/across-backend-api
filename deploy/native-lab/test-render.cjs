const assert=require('node:assert/strict');
const {render}=require('./render.cjs');
const run='cohost-0123456789abcdef';
const images=Object.fromEntries(['app','api','worker','postgres','redis','gateway','socat'].map(n=>[n,'sha256:'+'a'.repeat(64)]));
const doc=render({run,role:'main',images});
assert.equal(doc.networks.lab.internal,true);
assert.equal(doc.networks.lab.name,run+'-main_lab');
assert.equal(doc.networks.lab.ipam.config[0].ip_range,'172.29.248.128/25');
assert.equal(doc.services.gateway.networks.lab.ipv4_address,'172.29.248.10');
assert.deepEqual(doc.services.api1.command,['/app/across-api']);
assert.deepEqual(doc.services['message-fanout'].command,['/app/across-worker']);
assert.equal(doc.services['message-fanout'].environment.DB_MAX_CONNS,'2');
assert.throws(()=>render({run,role:'main',images:{...images,api:'latest'}}),/pinned release api/);
for(const s of Object.values(doc.services)) {
  assert.equal(s.ports,undefined);assert.equal(s.network_mode,undefined);
  assert.equal(s.labels['atlxpres.cohost.run'],run);
}
assert.equal(render({run,role:'database',images}).networks.default.internal,true);
console.log('Native network reservation and private services verified.');
