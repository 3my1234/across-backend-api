"""Original successful long-generator proof gates, not a capacity assertion."""
import hashlib,json,sys
from pathlib import Path

def check(directory):
    matches=list(directory.rglob('cpu-2-prewarm/gate.json'))
    if len(matches)!=1:raise ValueError('one retained generator proof required')
    p=matches[0]
    # Anchored to the already retained SHA256SUMS.json from run40866958782.
    # Text normalization matches the existing executed-client source check.
    if hashlib.sha256(p.read_text().encode()).hexdigest()!='85d1495779c37f5e19512a41922151fcebf389aefee88245b74132af744773e6':
        raise ValueError('retained generator gate checksum mismatch')
    gate=json.loads(p.read_text())
    if not gate['independent_generator_gate_passed'] or gate.get('requested_seconds')!=1800 or gate['measured_seconds']<1795:raise ValueError('long generator not qualified')
    if not all(gate['assertions'].values()) or gate.get('generator_memory_limit_bytes')!=4*2**30:raise ValueError('generator resource gates differ')
    actual=hashlib.sha256((p.parent/'executed-sessions.js').read_text().encode()).hexdigest()
    if actual!='dad72e9b2b8dd7dd9fca94bb6a0d6856d4081a49d301b53e3e133c127d01ea49':raise ValueError('different executed client')
    return gate

if __name__=='__main__':check(Path(sys.argv[1]))
