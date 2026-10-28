#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_INVESTIGATION_PHASE:-} == system && ${LAB_SESSION_TARGET:-} == 500 && ${LAB_SESSION_PROFILE:-} == stage ]]
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml -f deploy/notification-lab/system-compose.yml -f deploy/notification-lab/calibration-compose.yml "$@"; }
sql() { dc exec -T postgres psql -U lab -d capacity_lab -X -At -v ON_ERROR_STOP=1 -c "$1"; }
mkdir -p calibration-evidence
export LAB_CALIBRATION_SUITE_DIR="$PWD/session-exchange/calibration-suite"
mkdir -p "$LAB_CALIBRATION_SUITE_DIR"
cp deploy/session-lab/* "$LAB_CALIBRATION_SUITE_DIR/"
python3 deploy/notification-lab/generator_prewarm.py deploy/session-lab/sessions.js "$LAB_CALIBRATION_SUITE_DIR/sessions.js"
node --check "$LAB_CALIBRATION_SUITE_DIR/sessions.js"
cp "$LAB_CALIBRATION_SUITE_DIR/sessions.js" calibration-evidence/executed-sessions.js
sha256sum "$LAB_CALIBRATION_SUITE_DIR/"* >calibration-evidence/executed-suite.sha256
# Only synthetic credentials/snapshot in exchange; never uploaded.
test "$(sql 'SELECT marker FROM capacity_lab_identity;')" = disposable-compose-only
dc exec -T postgres pg_dump -U lab -Fc capacity_lab >session-exchange/system-baseline.dump
sha256sum session-exchange/system-baseline.dump >calibration-evidence/baseline.sha256
sha256sum session-exchange/fixtures.json >calibration-evidence/fixture.sha256
git rev-parse HEAD >calibration-evidence/source.txt
docker run --rm --network none atlxpres-capacity-lab:local sha256sum /app/api /app/lab >calibration-evidence/binaries.sha256
lscpu >calibration-evidence/runner-cpu.txt
free -h >calibration-evidence/runner-memory.txt
index=0
for mode in on; do
 index=$((index+1));arm="calibration-evidence/${index}-${mode}"
 mkdir -p "$arm"
 dc stop notification-worker notification-provider api1 api2 gateway >/dev/null 2>&1 || true
 dc rm -f notification-worker notification-provider >/dev/null
 # Fixed DB and internal network: never accept an operator-supplied host/name.
 test "$(sql 'SELECT marker FROM capacity_lab_identity;')" = disposable-compose-only
 dc exec -T postgres dropdb -U lab --force capacity_lab
 dc exec -T postgres createdb -U lab capacity_lab
 dc exec -T postgres pg_restore -U lab -d capacity_lab <session-exchange/system-baseline.dump
 sql 'ANALYZE;' >/dev/null
 dc exec -T redis redis-cli FLUSHDB >/dev/null
 if test -f session-exchange/provider-journal.jsonl; then mv session-exchange/provider-journal.jsonl "calibration-evidence/provider-previous-${index}.jsonl"; fi
 export LAB_NOTIFICATION_WORKERS=0
 if test "$mode" = on; then export LAB_NOTIFICATION_WORKERS=1; fi
 dc up -d --no-deps --wait --wait-timeout 180 api1 api2 gateway notification-provider
 if test "$mode" = on; then dc up -d notification-worker; fi
 sql 'SELECT pg_stat_statements_reset();' >/dev/null
 dc run --name atlxpres-system-observer --rm --no-deps exporter /app/lab observe-system >"$arm/occupancy.jsonl" 2>"$arm/observer.log" &
 observer=$!
 python3 deploy/notification-lab/cgroup_sampler.py >"$arm/cgroups.jsonl" 2>"$arm/cgroup-errors.log" &
 sampler=$!
 vmstat 1 >"$arm/host-vmstat.log" &
 host_sampler=$!
 trap 'kill "$host_sampler" 2>/dev/null || true; kill "$sampler" 2>/dev/null || true; docker stop -t 2 atlxpres-system-observer >/dev/null 2>&1 || true; wait "$observer" 2>/dev/null || true' EXIT
 telemetry=false
 for tick in $(seq 1 10); do
  if python3 - "$arm" <<'PYSAMPLER'
import json,sys
from pathlib import Path
p=Path(sys.argv[1])
cpu=[json.loads(s) for s in (p/'cgroups.jsonl').read_text().splitlines() if s.strip()]
assert cpu and all(any(name.endswith('-'+role+'-1') for name in cpu[-1]['containers']) for role in ('postgres','api1','api2'))
obs=[json.loads(s) for s in (p/'occupancy.jsonl').read_text().splitlines() if s.strip()]
assert obs and any('database_activity' in s for s in obs)
PYSAMPLER
  then telemetry=true; break; fi
  sleep 0.5
 done
 test "$telemetry" = true
 date -u +%FT%TZ >"$arm/start.txt"
 set +e
 dc run --rm --no-deps sessions >session-exchange/k6.log 2>&1
 measured=$?
 set -e
 date -u +%FT%TZ >"$arm/workload-end.txt"
 echo "$measured" >session-exchange/k6-exit-code.txt
 # Preserve the original 60-second readiness recovery observation.
 recovery=0
 for tick in $(seq 1 12); do
  date -u +%FT%TZ >>"$arm/recovery.log"
  dc exec -T api2 curl -fsS --max-time 3 -w '\n%{http_code} %{time_total}\n' http://gateway:8080/api/v1/ready >>"$arm/recovery.log" || recovery=1
  sleep 5
 done
 kill "$host_sampler" 2>/dev/null || true
 wait "$host_sampler" || true
 kill "$sampler" 2>/dev/null || true
 wait "$sampler" || true
 docker stop -t 2 atlxpres-system-observer >/dev/null 2>&1 || true
 wait "$observer" || true
 trap - EXIT
 date -u +%FT%TZ >"$arm/end.txt"
 sql "SELECT coalesce(json_agg(s),'[]') FROM(SELECT r.rolname,queryid::text,calls,total_exec_time,mean_exec_time,max_exec_time,rows,shared_blks_hit,shared_blks_read,temp_blks_written,query FROM pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY total_exec_time DESC LIMIT 150)s;" >"$arm/sql.json"
 set +e
 dc run --rm --no-deps verifier >"$arm/reconciliation.jsonl" 2>"$arm/reconciliation-errors.log"
 reconciled=$?
 set -e
 for file in k6.log summary.json timeseries.json k6-exit-code.txt; do if test -f "session-exchange/$file"; then mv "session-exchange/$file" "$arm/"; fi; done
 dc logs --no-color >"$arm/services.log"
 dc ps --all --format json >"$arm/containers.json"
 docker inspect $(dc ps -q) >"$arm/container-limits.json"
 if test -f session-exchange/provider-journal.jsonl; then cp session-exchange/provider-journal.jsonl "$arm/provider-journal.jsonl"; fi
 dc exec -T notification-provider curl -fsS http://localhost:8081/stats >"$arm/provider.json"
 if test "$mode" = on; then
  python3 - "$arm/provider.json" <<'PYWORKER'
import json,sys
d=json.load(open(sys.argv[1]))['channels']
assert all(d.get(c,{}).get('acknowledgements',0)>0 for c in ('email','push')), 'Actual worker/mock adapter did not complete any delivery'
PYWORKER
 fi
 sql "SELECT json_build_object('email',(SELECT coalesce(json_object_agg(status,n),'{}') FROM(SELECT status,count(*) n FROM email_outbox GROUP BY status)x),'push',(SELECT coalesce(json_object_agg(status,n),'{}') FROM(SELECT status,count(*) n FROM notification_push_deliveries GROUP BY status)x));" >"$arm/queues.json"
 printf '{"arm":%d,"workers":"%s","measured_exit":%d,"reconcile_exit":%d,"recovery_exit":%d,"qualification":false}\n' "$index" "$mode" "$measured" "$reconciled" "$recovery" >"$arm/result.json"
 # Same concurrency controls continue for diagnosis only, never to higher load.
done
echo 'Actual-stack headroom calibration completed. This is a bounded 180-second stage, NOT a 30-minute qualification. Inspect all arm exits, correctness and instrumentation overhead.' >calibration-evidence/collection.txt
python3 - <<'PYGATE'
import json
from pathlib import Path
rows=[json.loads(p.read_text()) for p in sorted(Path('calibration-evidence').glob('*/result.json'))]
assert len(rows)==1
assert all(r['measured_exit']==0 and r['reconcile_exit']==0 and r['recovery_exit']==0 for r in rows), 'Diagnostic collection retained; at least one original software gate failed. No capacity qualification.'
PYGATE
