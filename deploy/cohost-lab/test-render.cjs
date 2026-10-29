const assert=require('assert');const {render}=require('./render.cjs');
const images=Object.fromEntries(['app','postgres','redis','gateway','socat'].map(n=>[n,'example/'+n+'@sha256:'+'a'.repeat(64)]));
const args={run:'cohost-'+'b'.repeat(16),images};
const main=render({...args,role:'main'}),db=render({...args,role:'database'});
assert(!main.services.sessions && !main.services.runner);
for(const s of Object.values(main.services)){assert(s.cgroup_parent.endsWith('main.slice'));assert(s.labels['atlxpres.cohost.run']===args.run);assert(!s.build);assert(s.image.includes('@sha256:'));}
assert(main.networks.lab.internal);assert.deepEqual(main.services.gateway.ports,['127.0.0.1:58080:8080']);
assert(!JSON.stringify(main).includes('${'));assert(main.services.api1.environment.DB_MAX_CONNS==='8');
assert(main.services.api1.environment.HTTP_MAX_IN_FLIGHT==='64');
assert(main.services.api1.environment.TRUSTED_PROXY_CIDRS==='172.29.248.10/32,172.29.248.20/32,172.29.248.21/32,172.29.248.1/32');
assert(main.services.api1.environment.RATE_LIMIT_PER_MINUTE==='600');assert(db.services.postgres.cpus===1);
assert.deepEqual(db.services.postgres.ports,['127.0.0.1:55432:5432']);assert(db.volumes['synthetic-pg'].labels);assert(db.networks.default.internal);
assert.throws(()=>render({...args,run:'production',role:'main'}));
assert.throws(()=>render({...args,images:{...images,app:'latest'},role:'main'}));
assert.throws(()=>render({...args,role:'live'}));
console.log('Renderer isolation, digest, resource, preserved pool/admission and rejection checks passed.');
