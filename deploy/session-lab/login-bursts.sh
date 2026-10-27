#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_ACTOR_PAIRS:-} == 100 ]]
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml "$@"; }
python3 deploy/session-lab/sample-login-cgroups.py > session-evidence/login-cgroups.jsonl &
cpu_sampler=$!
trap 'kill "$cpu_sampler" 2>/dev/null || true' EXIT
date -u +%FT%TZ > session-evidence/burst-start.txt
set +e
dc run --name atlxpres-login-bursts --rm --no-deps runner /app/lab login-bursts > session-evidence/login-bursts.jsonl
result=$?
set -e
date -u +%FT%TZ > session-evidence/burst-end.txt
echo "$result" > session-evidence/burst-exit-code.txt
for tick in $(seq 1 12); do
 date -u +%FT%TZ >> session-evidence/burst-recovery.log
 dc exec -T api2 curl -fsS --max-time 3 -w '\n%{http_code} %{time_total}\n' http://gateway:8080/api/v1/ready >> session-evidence/burst-recovery.log || result=1
 sleep 5
done
exit "$result"
