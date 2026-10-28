#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_INVESTIGATION_PHASE:-} == ramp && ( ${LAB_SESSION_TARGET:-} == 500 || ${LAB_SESSION_TARGET:-} == 1000 ) && ${LAB_SESSION_PROFILE:-} == sustained && ${LAB_EXPANDED_JOURNEYS:-} == 1 ]]
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml "$@"; }
sql() { dc exec -T postgres psql -U lab -d capacity_lab -X -At -v ON_ERROR_STOP=1 -c "$1"; }
dc up -d --wait notification-provider
dc up -d notification-worker
sql 'SELECT pg_stat_statements_reset();' >/dev/null
date -u +%FT%TZ >session-evidence/ramp-sql-window-start.txt
sed 's@-f deploy/session-lab/compose.yml@-f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml@g' deploy/session-lab/qualify-ramp.sh >session-exchange/ramp-qualify.sh
bash session-exchange/ramp-qualify.sh
date -u +%FT%TZ >session-evidence/ramp-sql-window-end.txt
sql "SELECT coalesce(json_agg(s),'[]') FROM(SELECT r.rolname,queryid::text,calls,total_exec_time,mean_exec_time,max_exec_time,rows,query FROM pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY total_exec_time DESC LIMIT 100)s;" >session-evidence/ramp-sql-statements.json
# Reconcile writes before spending time on drain/recovery. Failed performance
# stages never proceed here; the workflow still retains their abort inventory.
dc run --rm --no-deps verifier >session-evidence/reconciliation.jsonl
node deploy/session-lab/advance.mjs session-exchange/summary.json session-evidence/reconciliation.jsonl >session-evidence/gate.json
drained=false
date -u +%FT%TZ >session-evidence/drain-start.txt
for tick in $(seq 1 61); do
 sql "SELECT json_build_object('time',clock_timestamp(),'email',(SELECT count(*) FROM email_outbox WHERE status IN('pending','retry','sending')),'push',(SELECT count(*) FROM notification_push_deliveries WHERE status IN('pending','retry','sending')));" >>session-evidence/ramp-send-drain.jsonl
 remaining=$(sql "SELECT (SELECT count(*) FROM email_outbox WHERE status IN('pending','retry','sending'))+(SELECT count(*) FROM notification_push_deliveries WHERE status IN('pending','retry','sending'));")
 if test "$remaining" -eq 0; then drained=true; break; fi
 sleep 5
done
date -u +%FT%TZ >session-evidence/drain-end.txt
echo "$drained" >session-evidence/send-backlog-drained.txt
dc run --rm --no-deps exporter /app/lab notification-verify >session-evidence/notification-final.json
dc logs --no-color notification-worker >session-evidence/worker.log
cp session-exchange/provider-journal.jsonl session-evidence/provider-journal.jsonl
test "$drained" = true
python3 - <<'PYACK'
import json
r=json.load(open('session-evidence/notification-final.json'))
assert not any(r['sent_without_mock_ack'].values())
assert set(r['email_status']) <= {'sent'}
assert set(r['push_status']) <= {'sent','delivered'}
PYACK
bash deploy/notification-lab/receipt-tail.sh
python3 - <<'PYRECEIPTS'
import json
r=json.load(open('session-evidence/notification-after-receipts.json'))
assert r['due_receipt_backlog']==0
assert set(r['push_status']) <= {'delivered'}
assert not any(r['sent_without_mock_ack'].values())
PYRECEIPTS
