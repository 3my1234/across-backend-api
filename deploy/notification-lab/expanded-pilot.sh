#!/usr/bin/env bash
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_INVESTIGATION_PHASE:-} == expanded && ${LAB_SESSION_TARGET:-} == 100 && ${LAB_SESSION_PROFILE:-} == stage && ${LAB_EXPANDED_JOURNEYS:-} == 1 ]]
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml "$@"; }
dc up -d --wait notification-provider
dc up -d notification-worker
sed 's@-f deploy/session-lab/compose.yml@-f deploy/session-lab/compose.yml -f deploy/notification-lab/compose.yml@g' deploy/session-lab/qualify-100.sh > session-exchange/expanded-qualify.sh
bash session-exchange/expanded-qualify.sh
dc run --rm --no-deps exporter /app/lab notification-verify > session-evidence/expanded-notifications.json
dc exec -T notification-provider curl -fsS http://localhost:8081/stats > session-evidence/expanded-provider.json
cp session-exchange/provider-journal.jsonl session-evidence/expanded-provider-journal.jsonl
# This is only a three-minute journey/accounting pilot. It cannot establish
# steady receipt capacity before the unchanged 15-minute eligibility window.
