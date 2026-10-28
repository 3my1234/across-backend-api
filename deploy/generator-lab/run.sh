#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_INVESTIGATION_PHASE:-} == system ]]
dc() { docker compose -f deploy/generator-lab/compose.yml "$@"; }
mkdir -p generator-exchange generator-evidence
git rev-parse HEAD >generator-evidence/source.txt
sha256sum deploy/session-lab/{sessions.js,policy.mjs,transport.mjs,established.mjs} >generator-evidence/unchanged-harness.sha256
lscpu >generator-evidence/runner-cpu.txt
free -h >generator-evidence/runner-memory.txt
cp deploy/generator-lab/{main.go,main_test.go,compose.yml,gate.py,run.sh,prewarm.py,test_prewarm.py} generator-evidence/
GENERATOR_LAB=synthetic-only deploy/generator-lab/generator-target prepare generator-exchange
chmod -R a+rwX generator-exchange
for mode in baseline prewarm; do
 quota=2
 export LAB_GENERATOR_SUITE_DIR="$PWD/generator-suite-${mode}"
 mkdir -p "$LAB_GENERATOR_SUITE_DIR"
 cp deploy/session-lab/* "$LAB_GENERATOR_SUITE_DIR/"
 if [[ "$mode" == prewarm ]]; then
   python3 deploy/generator-lab/prewarm.py deploy/session-lab/sessions.js "$LAB_GENERATOR_SUITE_DIR/sessions.js"
 fi
 node --check "$LAB_GENERATOR_SUITE_DIR/sessions.js"
 export LAB_GENERATOR_CPUS="$quota"
 arm="generator-evidence/cpu-${quota}-${mode}"
 mkdir -p "$arm"
 cp "$LAB_GENERATOR_SUITE_DIR/sessions.js" "$arm/executed-sessions.js"
 sha256sum "$LAB_GENERATOR_SUITE_DIR/"* >"$arm/executed-suite.sha256"
 dc down --remove-orphans >/dev/null 2>&1 || true
 dc up -d gateway
 dc run -d --name atlxpres-generator-sessions-measured --no-deps sessions >"$arm/container-id.txt"
 python3 deploy/notification-lab/cgroup_sampler.py >"$arm/cgroups.jsonl" 2>"$arm/sampler-errors.txt" &
 sampler=$!
 vmstat -t 1 >"$arm/host-vmstat.txt" &
 hostwatch=$!
 trap 'kill "$sampler" "$hostwatch" 2>/dev/null || true' EXIT
 docker wait atlxpres-generator-sessions-measured >"$arm/exit-code.txt"
 kill "$sampler" "$hostwatch" 2>/dev/null || true
 wait "$sampler" || true
 wait "$hostwatch" || true
 trap - EXIT
 docker logs atlxpres-generator-sessions-measured >"$arm/k6.log" 2>&1
 dc exec -T gateway wget -qO- http://localhost:8080/generator-stats >"$arm/target-stats.json"
 docker inspect atlxpres-generator-sessions-measured >"$arm/generator-inspect.json"
 for file in summary.json timeseries.json; do cp "generator-exchange/$file" "$arm/"; done
 set +e
 python3 deploy/generator-lab/gate.py "$arm" "$quota" >"$arm/gate-output.txt" 2>"$arm/gate-errors.txt"
 gate_exit=$?
 set -e
 echo "$gate_exit" >"$arm/gate-exit.txt"
 docker rm atlxpres-generator-sessions-measured >/dev/null
done
python3 - <<'PYGATE'
import json
from pathlib import Path
arms=[json.loads(p.read_text()) for p in Path('generator-evidence').glob('cpu-*/gate.json')]
assert len(arms)==2,'Both baseline and locally prepared arms must produce interpretable evidence'
assert json.loads(Path('generator-evidence/cpu-2-prewarm/gate.json').read_text())['independent_generator_gate_passed'],'Local preparation candidate did not qualify; do not repeat application500'
assert json.loads(Path('generator-evidence/cpu-2-baseline/gate.json').read_text())['assertions']['original_thresholds_pass'],'Baseline workload correctness failed'
PYGATE
