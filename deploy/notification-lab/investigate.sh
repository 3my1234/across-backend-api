#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_SESSION_TARGET:-} == 100 && ${LAB_SESSION_PROFILE:-} == sustained ]]
export COMPOSE_FILE=deploy/capacity-lab/compose.yml:deploy/session-lab/compose.yml:deploy/notification-lab/compose.yml
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml "$@"; }
sql() { dc exec -T postgres psql -U lab -d capacity_lab -X -At -v ON_ERROR_STOP=1 -c "$1"; }
dc up -d notification-worker
# Fail before the load phase if the actual SMTP/TLS or push adapter is broken.
sql "INSERT INTO notifications(user_id,type,title,body) SELECT buyer,'marketplace_message','Mock preflight','Synthetic only' FROM capacity_lab_actors ORDER BY slot LIMIT 1;"
ready=false
for tick in $(seq 1 30); do
 if [ "$(sql "SELECT count(DISTINCT channel) FROM capacity_lab_mock_delivery WHERE acknowledgements>0;")" -eq 2 ]; then ready=true; break; fi
 sleep 2
done
test "$ready" = true
bash deploy/notification-lab/faults.sh >session-evidence/faults.log 2>&1 &
faults=$!
trap 'kill "$faults" 2>/dev/null || true' EXIT
# Same performance profile/gates. Override only the Compose overlay selection.
sed 's@-f deploy/session-lab/compose.yml@-f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml@g' deploy/session-lab/qualify-100.sh >session-exchange/worker-qualify.sh
bash session-exchange/worker-qualify.sh
wait "$faults"
# Stop offering workload, then observe bounded send-backlog drain. Receipt
# acknowledgement has the real 15-minute eligibility/100-per-15s policy;
# report that separately rather than accelerating time or forcing zero.
date -u +%FT%TZ >session-evidence/drain-start.txt
drained=false
for tick in $(seq 1 60); do
 dc run --rm --no-deps exporter /app/lab notification-verify >>session-evidence/drain.jsonl
 remaining=$(sql "SELECT (SELECT count(*) FROM email_outbox WHERE status IN('pending','retry','sending'))+(SELECT count(*) FROM notification_push_deliveries WHERE status IN('pending','retry','sending'));")
 if [ "$remaining" -eq 0 ]; then drained=true; break; fi
 sleep 5
done
date -u +%FT%TZ >session-evidence/drain-end.txt
echo "$drained" >session-evidence/send-backlog-drained.txt
dc run --rm --no-deps exporter /app/lab notification-verify >session-evidence/notification-final.json
dc logs --no-color notification-worker >session-evidence/worker.log
# Investigation findings may include receipt backlog or repeated mock acceptance.
# Never describe those as exactly-once vendor/device delivery or hide them.
test "$drained" = true
