#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_INVESTIGATION_PHASE:-} == system ]]
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml -f deploy/notification-lab/query-compose.yml "$@"; }
mkdir -p query-evidence
git rev-parse HEAD >query-evidence/source.txt
dc stop api1 api2 gateway notification-worker >/dev/null 2>&1 || true
python3 deploy/notification-lab/cgroup_sampler.py >query-evidence/cgroups.jsonl 2>query-evidence/sampler.log &
sampler=$!
trap 'kill "$sampler" 2>/dev/null || true' EXIT
dc run --rm --no-deps exporter /app/lab query-diagnostic >query-evidence/queries.jsonl
kill "$sampler" 2>/dev/null || true
wait "$sampler" || true
trap - EXIT
dc exec -T postgres psql -U lab -d capacity_lab -X -At -v ON_ERROR_STOP=1 -c "SELECT coalesce(json_agg(s),'[]') FROM(SELECT queryid::text,calls,total_exec_time,mean_exec_time,shared_blks_hit,shared_blks_read,temp_blks_written,query FROM pg_stat_statements ORDER BY total_exec_time DESC LIMIT 100)s;" >query-evidence/sql.json
dc logs --no-color postgres >query-evidence/postgres.log
docker inspect $(dc ps -q) >query-evidence/container-limits.json
