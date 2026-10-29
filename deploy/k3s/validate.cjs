// Offline policy validator. No kubeconfig, subprocess, network or deployment path.
const fs = require('node:fs');
const path = require('node:path');
const YAML = require('yaml');
function ensure(ok, message) { if (!ok) throw Error(message); }
function documents(text) { return YAML.parseAllDocuments(text).map(d => { ensure(!d.errors.length, 'invalid YAML'); return d.toJSON(); }).filter(Boolean); }
function validate(text, release = false) {
 const docs = documents(text);
 const by = (kind, name) => docs.find(d => d.kind === kind && d.metadata.name === name);
 ensure(docs.length === 12, 'unexpected resource inventory');
 ensure(new Set(docs.map(d=>d.kind+'/'+d.metadata.name)).size === docs.length, 'duplicate resource');
 ensure(docs.every(d=>d.kind === 'Namespace' || d.metadata.namespace === 'atlxpres-staging'), 'isolated namespace required');
 ensure(!docs.some(d=>['Secret','ClusterRole','ClusterRoleBinding','PersistentVolume'].includes(d.kind)), 'secret or cluster-wide privilege not permitted');
 const ns=by('Namespace','atlxpres-staging'); ensure(ns.metadata.labels['pod-security.kubernetes.io/enforce']==='restricted','restricted namespace required');
 const cfg=by('ConfigMap','runtime-settings').data;
 for (const key of ['RUN_MIGRATIONS','RUN_INLINE_WORKERS','ENABLE_DEV_LOGIN','ENABLE_MOCK_PAYMENTS']) ensure(cfg[key]==='false', key+' must be false');
 ensure(cfg.ADMIN_BOOTSTRAP_TOKEN==='' && cfg.DATABASE_READ_URL==='' && cfg.DB_MAX_CONNS==='4','bounded config required');
 ensure(cfg.APP_ENV==='staging' && cfg.REDIS_OPTIONAL==='false','staging/dependency guard required');
 const api=by('Deployment','api'), worker=by('Deployment','notification-worker'), hpa=by('HorizontalPodAutoscaler','api');
 ensure(api.spec.replicas===2 && hpa.spec.minReplicas===2 && hpa.spec.maxReplicas===3,'API replica cap required');
 ensure(api.spec.strategy.rollingUpdate.maxSurge===0 && api.spec.strategy.rollingUpdate.maxUnavailable===1,'three-node safe rollout required');
 ensure(worker.spec.replicas===0,'worker transport qualification required before activation');
 const pods=[api,worker].map(d=>d.spec.template.spec);
 for (const p of pods) {
  ensure(p.automountServiceAccountToken===false && p.serviceAccountName==='runtime','no API token required');
  ensure(!p.hostNetwork && !p.hostPID && !p.nodeName,'host access forbidden');
  ensure(p.terminationGracePeriodSeconds>=90 && p.securityContext.runAsNonRoot===true,'drain/nonroot required');
  for(const c of p.containers) {
   ensure(/^registry\.example\.invalid\/atlxpres\/(api|worker)@sha256:[0-9a-f]{64}$/.test(c.image),'reviewed placeholder digest required');
   ensure(c.securityContext.allowPrivilegeEscalation===false && c.securityContext.readOnlyRootFilesystem===true && c.securityContext.capabilities.drop.includes('ALL'),'container hardening required');
   ensure(c.livenessProbe.httpGet.path==='/api/v1/health' && c.readinessProbe.httpGet.path==='/api/v1/ready','distinct dependency probes required');
   ensure(c.resources.requests && c.resources.limits,'resource bounds required');
  }
 }
 ensure(pods[0].affinity.podAntiAffinity.requiredDuringSchedulingIgnoredDuringExecution[0].topologyKey==='kubernetes.io/hostname','hard host separation required');
 ensure(pods[0].containers[0].command[0]==='/app/across-api' && pods[1].containers[0].command[0]==='/app/across-worker','actual image commands required');
 ensure(by('Service','api').spec.type==='ClusterIP','private service required');
 ensure(by('PodDisruptionBudget','api').spec.minAvailable===1,'voluntary disruption floor required');
 const deny=by('NetworkPolicy','default-deny').spec;
 ensure(deny.policyTypes.includes('Ingress') && deny.policyTypes.includes('Egress') && !deny.ingress && !deny.egress,'default-deny required');
 for(const d of docs.filter(d=>d.kind==='NetworkPolicy')) {
  const t=JSON.stringify(d.spec); ensure(!t.includes('0.0.0.0/0') && !t.includes('::/0'),'universal network range forbidden');
 }
 ensure(!text.includes('173.212.219.169') && !text.includes('79.143.176.173') && !text.includes('10.77.0.2'), 'current production endpoint forbidden');
 // This package intentionally has unresolved identity/dependency placeholders.
 if(release) ensure(!text.includes('.invalid') && !text.includes('192.0.2.') && !text.includes('0000000000000000000000000000000000000000000000000000000000000000'), 'unresolved placeholders: package is NOT releasable');
 return {resources:docs.length, mode:'offline-policy-only', api_max_connections:12, future_single_worker_connections:4, migrator_connections:2, production_qualified:false};
}
if(require.main===module) {
 try { const text=fs.readFileSync(path.join(__dirname,'staging.yaml'),'utf8'); console.log(JSON.stringify(validate(text,process.argv.includes('--release')),null,2)); }
 catch(e) { console.error(e.message); process.exitCode=1; }
}
module.exports={validate,documents};
