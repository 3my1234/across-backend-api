"""Original successful long-generator proof gates, not a capacity assertion."""
import hashlib,json,sys
from pathlib import Path

def check(directory):
    p=next(directory.rglob('cpu-2-prewarm/gate.json'))
    gate=json.loads(p.read_text())
    if not gate['independent_generator_gate_passed'] or gate.get('requested_seconds')!=1800 or gate['measured_seconds']<1795:raise ValueError('long generator not qualified')
    if not all(gate['assertions'].values()) or gate.get('generator_memory_limit_bytes')!=4*2**30:raise ValueError('generator resource gates differ')
    actual=hashlib.sha256((p.parent/'executed-sessions.js').read_text().encode()).hexdigest()
    if actual!='dad72e9b2b8dd7dd9fca94bb6a0d6856d4081a49d301b53e3e133c127d01ea49':raise ValueError('different executed client')
    return gate

if __name__=='__main__':check(Path(sys.argv[1]))
