#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_INVESTIGATION_PHASE:-} == system && ${LAB_CALIBRATION_DB_MEMORY:-} == 8g ]]
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml -f deploy/notification-lab/system-compose.yml -f deploy/notification-lab/calibration-compose.yml "$@"; }
sql() { dc exec -T postgres psql -U lab -d capacity_lab -X -At -v ON_ERROR_STOP=1 -c "$1"; }
out=calibration-evidence/trigger-profile
mkdir -p "$out"
test "$(sql 'SELECT marker FROM capacity_lab_identity;')" = disposable-compose-only
# No API, workers or load generator during CPU scaling. No ports or real providers.
dc stop api1 api2 gateway notification-worker notification-provider >/dev/null 2>&1 || true
sql "SELECT r.id,r.owner_user_id FROM chat_rooms r WHERE (SELECT count(*) FROM chat_participants p WHERE p.room_id=r.id AND p.state='active' AND p.user_id<>r.owner_user_id)=10 ORDER BY r.id LIMIT 1" >"$out/actors.txt"
python3 deploy/notification-lab/trigger_profile.py "$out"
sha256sum migrations/*.sql >"$out/migrations.sha256"
sql "SELECT coalesce(json_agg(x),'[]') FROM(SELECT tgname,pg_get_triggerdef(oid) AS definition FROM pg_trigger WHERE tgrelid='notifications'::regclass AND NOT tgisinternal)x" >"$out/triggers.json"
sql "SELECT coalesce(json_agg(x),'[]') FROM(SELECT indexname,indexdef FROM pg_indexes WHERE tablename IN('notifications','user_push_tokens','notification_push_deliveries','web_push_subscriptions','web_push_deliveries'))x" >"$out/indexes.json"
sql 'ANALYZE;' >/dev/null
sql "SELECT json_build_array((SELECT count(*) FROM notifications),(SELECT count(*) FROM notification_push_deliveries),(SELECT count(*) FROM web_push_deliveries))" >"$out/before-counts.json"
container=$(dc ps -q postgres)
docker cp "$out/pgbench.sql" "$container:/tmp/trigger-pgbench.sql"
docker cp "$out/plan.sql" "$container:/tmp/trigger-plan.sql"
python3 deploy/notification-lab/cgroup_sampler.py >"$out/cgroups.jsonl" 2>"$out/sampler.log" &
sampler=$!
trap 'kill "$sampler" 2>/dev/null || true; wait "$sampler" 2>/dev/null || true' EXIT
# Matched ABBA CPU allocation; no application capacity or production-equivalence claim.
sql "SELECT pg_get_functiondef('notification_sender_identity(jsonb)'::regprocedure)" >"$out/original-function.sql"
if [[ ${LAB_PROFILE_SENDER:-0} == 1 ]]; then
 sql 'CREATE SCHEMA profile_baseline;' >/dev/null
 sed 's/public.notification_sender_identity(/profile_baseline.notification_sender_identity(/' "$out/original-function.sql" | dc exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U lab -d capacity_lab
 cp deploy/notification-lab/sender-candidate.sql "$out/candidate-function.sql"
fi
arm=0
budgets=(2 4 4 2)
if [[ ${LAB_PROFILE_SENDER:-0} == 1 ]]; then budgets=(2 2 2 2); fi
for cpus in "${budgets[@]}"; do
 arm=$((arm+1)); prefix="$out/arm-$arm-cpu-$cpus"
 if [[ ${LAB_PROFILE_SENDER:-0} == 1 ]]; then
  definition="$out/original-function.sql"
  if [[ $arm == 2 || $arm == 3 ]]; then definition="$out/candidate-function.sql"; fi
  dc exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U lab -d capacity_lab <"$definition"
  echo "$definition" >"$prefix-function.txt"
  sql "WITH cases AS (SELECT jsonb_build_object('sender_user_id',a.buyer,'provider_id',a.provider,'sender_is_provider',v) AS data FROM capacity_lab_actors a CROSS JOIN unnest(ARRAY['true','false','',NULL]) v UNION ALL SELECT jsonb_build_object('sender_user_id',a.provider_user,'provider_id',a.provider,'sender_is_provider',v) FROM capacity_lab_actors a CROSS JOIN unnest(ARRAY['true','false','',NULL]) v UNION ALL SELECT jsonb_build_object('sender_user_id',a.provider_user,'provider_id','invalid','sender_is_provider','true') FROM capacity_lab_actors a UNION ALL SELECT x FROM unnest(ARRAY['{}'::jsonb,NULL::jsonb,'{\"sender_user_id\":\"invalid\"}'::jsonb,'{\"sender_user_id\":null,\"sender_is_provider\":true}'::jsonb]) x) SELECT json_build_object('cases',count(*),'mismatches',count(*) FILTER(WHERE notification_sender_identity(data) IS DISTINCT FROM profile_baseline.notification_sender_identity(data))) FROM cases" >"$prefix-equality.json"
  python3 - "$prefix-equality.json" <<'PYEQUALITY'
import json,sys
r=json.load(open(sys.argv[1]));assert r['cases']>=4000 and r['mismatches']==0, 'Sender identity result changed'
PYEQUALITY
 fi
 docker update --cpus "$cpus" "$container" >/dev/null
 docker inspect "$container" >"$prefix-limits.json"
 date -u +%FT%TZ >"$prefix-start.txt"
 dc exec -T postgres psql -X -qAt -v ON_ERROR_STOP=1 -U lab -d capacity_lab -f /tmp/trigger-plan.sql >"$prefix-plan.json"
 docker run --rm --name atlxpres-trigger-client --network atlxpres-capacity-lab_lab --cpus 1 --memory 256m --label com.docker.compose.project=atlxpres-capacity-lab -e PGPASSWORD=lab-disposable-only -v "$PWD/$out/pgbench.sql:/query.sql:ro" postgres:16-alpine pgbench -h postgres -U lab -d capacity_lab -n -c 8 -j 2 -T 20 -r -f /query.sql >"$prefix-pgbench.txt" 2>"$prefix-errors.txt"
 date -u +%FT%TZ >"$prefix-end.txt"
done
docker update --cpus 2 "$container" >/dev/null
if [[ ${LAB_PROFILE_SENDER:-0} == 1 ]]; then
 dc exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U lab -d capacity_lab <"$out/original-function.sql"
fi
sql "SELECT json_build_array((SELECT count(*) FROM notifications),(SELECT count(*) FROM notification_push_deliveries),(SELECT count(*) FROM web_push_deliveries))" >"$out/after-counts.json"
cmp "$out/before-counts.json" "$out/after-counts.json"
kill "$sampler" 2>/dev/null || true; wait "$sampler" || true; trap - EXIT
python3 - <<'PY'
import json
from pathlib import Path
p=Path('calibration-evidence/trigger-profile')
for f in p.glob('*-plan.json'):
    plan=json.loads(f.read_text())[0]
    assert any(t.get('Trigger Name')=='notifications_enqueue_push_deliveries' and t['Calls']==10 for t in plan['Triggers']), 'Actual native trigger not exercised for all recipients'
    assert any(t.get('Trigger Name')=='user_web_push_notification' and t['Calls']==10 for t in plan['Triggers']), 'Actual web trigger not exercised for all recipients'
    print(f.name, json.dumps({'execution_ms':plan['Execution Time'],'triggers':plan['Triggers']}))
PY
echo 'Focused real-schema trigger/CPU comparison only; not sustained500 qualification.' >"$out/scope.txt"
