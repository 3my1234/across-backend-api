"""Generate an isolated candidate with local-only VU preparation. Baseline stays unchanged."""
from pathlib import Path
import sys

METRICS="""
// Harness-only preparation: no HTTP, application actions, randomness or startup counters.
const localPrepared=new Counter('local_generator_prepared'),localUsed=new Counter('local_generator_used');
let preparedBinding=null;
options.scenarios.local_initialization={executor:'per-vu-iterations',vus:cfg.target,iterations:1,maxDuration:'8s',gracefulStop:'0s',exec:'initializeGenerator',tags:{phase:'generator_init'}};
options.scenarios.sessions.startTime='10s';
options.thresholds.local_generator_prepared=[`count==${cfg.target}`];
options.thresholds.local_generator_used=[`count==${cfg.target}`];
export function initializeGenerator(data){
 if(!established||preparedBinding!==null)exec.test.abort('Local preparation requires fresh established VU');
 const original={...sessions[exec.vu.idInTest-1]};
 if(!original.id)exec.test.abort('Missing local synthetic identity');
 const bound=bindEstablished(original,data.authenticated,cfg.target);
 preparedBinding={id:bound.id,token:bound.token,vu:exec.vu.idInTest};
 localPrepared.add(1);
}
"""
ORIGINAL="if(established){actor=bindEstablished(actor,data.authenticated,cfg.target);}else{"
REPLACEMENT="""if(established){
   if(!preparedBinding||preparedBinding.id!==actor.id||preparedBinding.vu!==exec.vu.idInTest||!preparedBinding.token)exec.test.abort('Local VU preparation/cache/identity mismatch');
   actor={...actor,token:preparedBinding.token};localUsed.add(1);
  }else{"""
ANCHOR="let actor=null,loggedIn=false"

def candidate(source):
 if source.count(ANCHOR)!=1 or source.count(ORIGINAL)!=1:raise ValueError('Reviewed baseline anchors changed; stop')
 return source.replace(ANCHOR,METRICS+'\n'+ANCHOR,1).replace(ORIGINAL,REPLACEMENT,1)

def recover(output):
 return output.replace(METRICS+'\n','',1).replace(REPLACEMENT,ORIGINAL,1)

if __name__=='__main__':
 source=Path(sys.argv[1]).read_text();output=candidate(source)
 assert recover(output)==source,'Whitelisted patch must be reversible to exact baseline'
 Path(sys.argv[2]).write_text(output)
