#!/usr/bin/env bash
# Existing500 measurement, verifier, drain and receipt-tail bodies are unchanged.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_SESSION_TARGET:-} == 500 && ${LAB_SESSION_PROFILE:-} == sustained && ${LAB_INVESTIGATION_PHASE:-} == ramp ]]
mkdir -p session-exchange session-evidence
python3 - <<'PYGLUE'
from pathlib import Path
extra=' -f deploy/notification-lab/system-compose.yml -f deploy/notification-lab/calibration-compose.yml'
needle='-f deploy/notification-lab/compose.yml'
for name in ('ramp.sh','receipt-tail.sh'):
    original=Path('deploy/notification-lab',name).read_text()
    generated=original.replace(needle,needle+extra)
    if name=='ramp.sh':
        generated=generated.replace('bash deploy/notification-lab/receipt-tail.sh','bash session-exchange/qualified-receipt-tail.sh')
    Path('session-exchange/qualified-'+name).write_text(generated)
# Record source and generated glue. No client, policy or measurement body edits.
PYGLUE
sha256sum deploy/session-lab/{sessions.js,policy.mjs,qualify-ramp.sh} deploy/notification-lab/{ramp.sh,receipt-tail.sh} session-exchange/qualified-{ramp,receipt-tail}.sh >session-evidence/qualified-glue.sha256
LAB_INVESTIGATION_PHASE=system python3 deploy/notification-lab/cgroup_sampler.py >session-evidence/cgroups.jsonl 2>session-evidence/cgroup-errors.log &
cgroup=$!
vmstat -t 1 >session-evidence/host-vmstat.log &
host=$!
trap 'kill "$cgroup" "$host" 2>/dev/null || true; wait "$cgroup" "$host" 2>/dev/null || true' EXIT
bash session-exchange/qualified-ramp.sh
