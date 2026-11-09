// Native topology: retain existing services/caps, reserve the original generator IP.
const {render: original} = require('../cohost-lab/render.cjs');
function render(input) {
  const doc = original(input);
  if (input.role === 'main') {
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
