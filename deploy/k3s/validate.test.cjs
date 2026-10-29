const test=require('node:test'), assert=require('node:assert/strict'), fs=require('node:fs'), path=require('node:path');
const {validate}=require('./validate.cjs');
const source=fs.readFileSync(path.join(__dirname,'staging.yaml'),'utf8').replaceAll('\r\n','\n');
test('base policy is valid preparation, never release',()=>{assert.equal(validate(source).resources,12);assert.throws(()=>validate(source,true),/placeholder/);});
for(const [name,from,to] of [
 ['startup migration',"RUN_MIGRATIONS: 'false'","RUN_MIGRATIONS: 'true'"],
 ['live worker','replicas: 0','replicas: 1'],
 ['unbounded API','maxReplicas: 3','maxReplicas: 30'],
 ['fourth surge pod','maxSurge: 0','maxSurge: 1'],
 ['production DB','192.0.2.10/32','10.77.0.2/32'],
 ['token mounting','automountServiceAccountToken: false','automountServiceAccountToken: true'],
 ['wrong binary','/app/across-worker','/app/across-api'],
 ['dependency liveness','livenessProbe:\n            httpGet: {path: /api/v1/health','livenessProbe:\n            httpGet: {path: /api/v1/ready'],
]) test('reject '+name,()=>{assert.notEqual(source.replaceAll(from,to),source);assert.throws(()=>validate(source.replaceAll(from,to)));});

test('image command and shutdown assumptions match current source',()=>{
 const root=path.join(__dirname,'../..');
 const docker=fs.readFileSync(path.join(root,'Dockerfile'),'utf8');
 const worker=fs.readFileSync(path.join(root,'Dockerfile.worker'),'utf8');
 const main=fs.readFileSync(path.join(root,'cmd/api/main.go'),'utf8');
 assert.ok(docker.includes('/app/across-api') && docker.includes('/app/across-migrate'));
 assert.ok(worker.includes('/app/across-worker'));
 assert.ok(main.includes('time.Sleep(3 * time.Second)') && main.includes('65*time.Second'));
 const migration=fs.readFileSync(path.join(__dirname,'migration-job.example.yaml'),'utf8');
 assert.ok(migration.includes('backoffLimit: 0') && migration.includes('/app/across-migrate'));
});
