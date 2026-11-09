// Native topology: retain existing services/caps, reserve the original generator IP.
const {render: original} = require('../cohost-lab/render.cjs');
function render(input) {
  const doc = original(input);
  if (input.role === 'main') {
    for(const key of ['api','worker']) if(!/^(?:[a-zA-Z0-9./:_-]+@)?sha256:[a-f0-9]{64}$/.test(input.images[key]||'')) throw Error('pinned release '+key+' image required');
    for(const name of ['api1','api2']) {
      doc.services[name].image=input.images.api;
      doc.services[name].command=['/app/across-api'];
      doc.services[name].environment.ASYNC_MESSAGE_NOTIFICATIONS='true';
    }
    const worker=structuredClone(doc.services.api1);
    worker.image=input.images.worker;worker.command=['/app/across-worker'];
    worker.cpus=.25;worker.mem_limit='256m';worker.memswap_limit='256m';
    delete worker.healthcheck;
    worker.environment={...worker.environment,WORKER_ROLES:'message_fanout',DB_MAX_CONNS:'2',DB_MIN_CONNS:'1',WORKER_HEALTH_ADDR:':9090',DATABASE_URL:worker.environment.DATABASE_URL.replace('system-api1','system-message-fanout')};
    doc.services['message-fanout']=worker;
    doc.networks.lab.name = input.run+'-main_lab';
    doc.networks.lab.ipam.config[0].ip_range = '172.29.248.128/25';
    // No published port or host networking is needed by the local generator.
    for (const s of Object.values(doc.services)) {
      if (s.ports || s.network_mode) throw Error('native services must remain private');
    }
  }
  return doc;
}
if(require.main===module) console.log(JSON.stringify(render(JSON.parse(require('fs').readFileSync(process.argv[2],'utf8'))),null,2));
module.exports={render};
