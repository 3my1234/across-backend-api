#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_INVESTIGATION_PHASE:-} == receipts && ${LAB_SESSION_TARGET:-} == 100 ]]
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml "$@"; }
sql() { dc exec -T postgres psql -U lab -d capacity_lab -X -At -v ON_ERROR_STOP=1 -c "$1"; }
# Real wall-clock eligibility: do not age sent_at, shorten 15 minutes, or replay
# notification sends. Observe all remaining receipts after the load has stopped.
date -u +%FT%TZ > session-evidence/receipt-tail-start.txt
complete=false
for tick in $(seq 1 241); do
 sql "SELECT json_build_object('time',clock_timestamp(),'pending',count(*),'eligible',count(*) FILTER(WHERE sent_at<=clock_timestamp()-interval '15 minutes'),'oldest_eligible_seconds',coalesce(max(extract(epoch FROM clock_timestamp()-sent_at)-900) FILTER(WHERE sent_at<=clock_timestamp()-interval '15 minutes'),0),'claimed',count(*) FILTER(WHERE receipt_claim IS NOT NULL),'attempts',coalesce(sum(receipt_attempts),0)) FROM notification_push_deliveries WHERE status='sent' AND receipt_checked_at IS NULL;" >> session-evidence/receipt-tail.jsonl
 remaining=$(sql "SELECT count(*) FROM notification_push_deliveries WHERE status='sent' AND receipt_checked_at IS NULL;")
 if test "$remaining" -eq 0; then complete=true; break; fi
 sleep 5
done
date -u +%FT%TZ > session-evidence/receipt-tail-end.txt
echo "$complete" > session-evidence/receipts-drained.txt
dc run --rm --no-deps exporter /app/lab notification-verify > session-evidence/notification-after-receipts.json
dc exec -T notification-provider curl -fsS http://localhost:8081/stats > session-evidence/provider-after-receipts.json
cp session-exchange/provider-journal.jsonl session-evidence/provider-journal-after-receipts.jsonl
dc logs --no-color notification-worker > session-evidence/worker-after-receipts.log
test "$complete" = true
