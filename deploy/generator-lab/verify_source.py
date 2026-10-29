"""Pin the unchanged workload and exact local-preparation transform to passed evidence."""
import hashlib,json
from pathlib import Path
from prewarm import candidate,recover

def verify():
 source=(Path(__file__).parents[1]/'session-lab/sessions.js').read_text()
 output=candidate(source)
 baseline=hashlib.sha256(source.encode()).hexdigest()
 prepared=hashlib.sha256(output.encode()).hexdigest()
 assert baseline=='db3013f473b41fc7d35e01d7385d620b247b977e3dd86e9e8683d14a4bb337a3','Qualified baseline workload changed'
 assert prepared=='dad72e9b2b8dd7dd9fca94bb6a0d6856d4081a49d301b53e3e133c127d01ea49','Qualified preparation transform changed'
 assert recover(output)==source,'Preparation must recover exact baseline'
 return {'proof_run':40747865966,'proof_source':'41ccc8344a6974d43126b96343746bc4f4ed76d1','normalized_baseline_sha256':baseline,'normalized_prepared_sha256':prepared,'normalization':'UTF-8 text with LF line endings only; no whitespace/content changes','duration_change':'Environment selects existing stage180 or sustained1800 policy; executed client bytes unchanged'}

if __name__=='__main__':print(json.dumps(verify(),indent=2))
