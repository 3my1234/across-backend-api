#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_SESSION_TARGET:-} == 100 && ${LAB_SESSION_PROFILE:-} == stage ]]
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml "$@"; }
sql() { dc exec -T postgres psql -U lab -d capacity_lab -X -At -v ON_ERROR_STOP=1 -c "$1"; }
mkdir -p comparison-evidence
# Restore the SAME byte-for-byte database image before each measured run.
dc exec -T postgres pg_dump -U lab -Fc capacity_lab > session-exchange/comparison-baseline.dump
sha256sum session-exchange/comparison-baseline.dump > comparison-evidence/baseline.sha256
cp session-evidence/* comparison-evidence/ 2>/dev/null || true
# Adapter preflight is outside measurement; its SQL effects are discarded by
# restoring the baseline before run1. No production target exists in this lab.
dc up -d --wait notification-provider
dc up -d notification-worker
sql "INSERT INTO notifications(user_id,type,title,body) SELECT buyer,'marketplace_message','Mock preflight','Synthetic only' FROM capacity_lab_actors ORDER BY slot LIMIT 1;"
ready=false
for tick in $(seq 1 30); do
 dc exec -T notification-provider curl -fsS http://localhost:8081/stats > comparison-evidence/mock-preflight.json
 if python3 -c 'import json;d=json.load(open("comparison-evidence/mock-preflight.json"))["channels"];assert all(d.get(c,{}).get("acknowledgements",0)>0 for c in ("email","push"))'; then ready=true;break;fi
 sleep 2
done
test "$ready" = true
index=0
for mode in off on on off; do
 index=$((index+1));stage="${index}-${mode}"
 dc stop notification-worker notification-provider api1 api2 gateway || true
 dc rm -f notification-worker notification-provider >/dev/null
 dc exec -T postgres dropdb -U lab --force capacity_lab
 dc exec -T postgres createdb -U lab capacity_lab
 dc exec -T postgres pg_restore -U lab -d capacity_lab < session-exchange/comparison-baseline.dump
 dc exec -T redis redis-cli FLUSHDB >/dev/null
 # Explicit known disposable journal only; preserve every previous stage first.
 if test -f session-exchange/provider-journal.jsonl; then mv session-exchange/provider-journal.jsonl "comparison-evidence/provider-previous-${index}.jsonl"; fi
 export LAB_NOTIFICATION_WORKERS=0
 if test "$mode" = on; then export LAB_NOTIFICATION_WORKERS=1; fi
 mkdir -p "comparison-evidence/$stage"
 # Evidence filenames are reused by the original harness; retain then reset.
 find session-evidence -maxdepth 1 -type f -delete
 for file in summary.json timeseries.json k6.log k6-exit-code.txt; do rm -f "session-exchange/$file"; done
 dc up -d --no-deps --wait --wait-timeout 180 api1 api2 gateway notification-provider
 if test "$mode" = on; then dc up -d notification-worker; fi
 sql 'SELECT pg_stat_statements_reset();' >/dev/null
 date -u +%FT%TZ > session-evidence/comparison-start.txt
 # No pre-warming of inbox: include the initial arrival spike in both arms.
 sed 's@-f deploy/session-lab/compose.yml@-f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml@g' deploy/session-lab/qualify-100.sh > session-exchange/worker-qualify.sh
 set +e
 bash session-exchange/worker-qualify.sh
 measured=$?
 set -e
 # Capture SQL before reconciliation or EXPLAIN can contaminate the counters.
 sql "SELECT coalesce(json_agg(s),'[]') FROM(SELECT r.rolname,queryid::text,calls,total_exec_time,mean_exec_time,max_exec_time,rows,shared_blks_hit,shared_blks_read,temp_blks_written,query FROM pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY total_exec_time DESC LIMIT 80)s;" > session-evidence/sql-statements.json
 set +e
 dc run --rm --no-deps verifier > session-evidence/reconciliation.jsonl 2>session-evidence/reconciliation-errors.log
 reconciled=$?
 dc run --rm --no-deps exporter /app/lab notification-verify > session-evidence/notification-final.json
 verified=$?
 node deploy/session-lab/advance.mjs session-exchange/summary.json session-evidence/reconciliation.jsonl > session-evidence/gate.json 2>session-evidence/gate-errors.log
 gated=$?
 set -e
 cp session-exchange/{k6.log,summary.json,timeseries.json} session-evidence/
 dc logs --no-color > session-evidence/services.log
 dc logs --no-color notification-worker > session-evidence/worker.log
 cp session-exchange/provider-journal.jsonl session-evidence/provider-journal.jsonl
 python3 deploy/notification-lab/explain-inbox.py > session-exchange/explain-inbox.sql
 dc exec -T postgres psql -U lab -d capacity_lab -X -v ON_ERROR_STOP=1 < session-exchange/explain-inbox.sql > session-evidence/inbox-plans.txt
 printf '{"measured_exit":%d,"reconcile_exit":%d,"notification_verify_exit":%d,"gate_exit":%d}\n' "$measured" "$reconciled" "$verified" "$gated" > session-evidence/exits.json
 cp -R session-evidence/. "comparison-evidence/$stage/"
 # No fault tests or higher stage after any failed prerequisite.
 if test "$measured" -ne 0 || test "$reconciled" -ne 0 || test "$verified" -ne 0 || test "$gated" -ne 0; then
  echo "STOP: $stage failed; preserve evidence, no outage/restart phase."
  exit 1
 fi
done
echo 'Four matched short stages passed. Fault/recovery qualification remains a separate phase.' > comparison-evidence/comparison-result.txt
