#!/usr/bin/env bash
set -euo pipefail
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml "$@"; }
sql() { dc exec -T postgres psql -U lab -d capacity_lab -X -At -v ON_ERROR_STOP=1 -c "$1"; }
control() { dc exec -T notification-provider curl -fsS -X POST -H 'Content-Type: application/json' --data "$1" http://localhost:8081/control; }
sleep 180
date -u +%FT%TZ
echo 'Mock SMTP451/push503 outage, 25 seconds; real retry timings unchanged.'
control '{"outage_seconds":25}'
sleep 20
sql "SELECT json_build_object('time',now(),'email_retry',(SELECT count(*) FROM email_outbox WHERE status='retry'),'push_retry',(SELECT count(*) FROM notification_push_deliveries WHERE status='retry'));"
sleep 280
control '{"delay_ms":40000}'
sleep 5
sql "INSERT INTO capacity_lab_killed_jobs SELECT 'email',id::text,locked_at FROM email_outbox WHERE status='sending' UNION ALL SELECT 'push',notification_id::text||':'||push_token_id::text,updated_at FROM notification_push_deliveries WHERE status='sending';"
date -u +%FT%TZ
sql "SELECT channel,count(*) FROM capacity_lab_killed_jobs GROUP BY channel;"
test "$(sql 'SELECT count(*) FROM capacity_lab_killed_jobs;')" -gt 0
dc kill -s SIGKILL notification-worker
control '{"delay_ms":0}'
dc start notification-worker
sleep 30
date -u +%FT%TZ
dc run --rm --no-deps exporter /app/lab notification-verify
sleep 300
date -u +%FT%TZ
dc run --rm --no-deps exporter /app/lab notification-verify
control '{"ambiguous_email":1,"ambiguous_push":1}'
echo 'Inject one accepted-but-lost acknowledgement per mock transport; record resubmissions without claiming real vendor deduplication.'
