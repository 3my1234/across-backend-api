#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_SESSION_TARGET:-} == 100 ]]
[[ ${LAB_SESSION_PROFILE:-} == stage || ${LAB_SESSION_PROFILE:-} == sustained ]]
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml "$@"; }
# Observer is outside k6; private bearer credentials never enter the artifacts.
dc run --name atlxpres-session-observer --rm --no-deps exporter /app/lab observe-sessions > session-evidence/pools.jsonl 2>session-evidence/pool-observer.log &
observer=$!
(
 while true; do
  date -u +%FT%TZ
  docker stats --no-stream --format '{{json .}}'
  dc exec -T postgres psql -U lab -d capacity_lab -X -At -c "SELECT json_build_object('time',now(),'clients',count(*),'active',count(*) FILTER(WHERE state='active'),'lock_waiters',count(*) FILTER(WHERE wait_event_type='Lock')) FROM pg_stat_activity WHERE backend_type='client backend';"
  dc exec -T redis redis-cli INFO memory
  dc exec -T redis redis-cli INFO stats
  sleep 5
 done
) > session-evidence/resources.log 2>&1 &
sampler=$!
trap 'kill "$sampler" 2>/dev/null || true; docker stop -t 2 atlxpres-session-observer >/dev/null 2>&1 || true; wait "$observer" 2>/dev/null || true' EXIT
date -u +%FT%TZ > session-evidence/measurement-start.txt
set +e
dc run --rm --no-deps sessions > session-exchange/k6.log 2>&1
result=$?
set -e
date -u +%FT%TZ > session-evidence/measurement-end.txt
echo "$result" > session-evidence/k6-exit-code.txt
# Observe readiness continuously through the audit's 60-second recovery window.
for tick in $(seq 1 12); do
 date -u +%FT%TZ >> session-evidence/recovery.log
 dc exec -T api2 curl -fsS --max-time 3 -w '\n%{http_code} %{time_total}\n' http://gateway:8080/api/v1/ready >> session-evidence/recovery.log || result=1
 sleep 5
done
exit "$result"
