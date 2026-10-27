#!/usr/bin/env bash
# Existing public-repository standard runner, fixed internal lab only.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${LAB_COMPARISON:-} == true ]]
[[ ${LAB_SCENARIO:-} == large-busy && ${LAB_DB_CPUS:-} == 2 ]]
dc() { docker compose -f deploy/capacity-lab/compose.yml -f deploy/capacity-lab/comparison-compose.yml "$@"; }
mkdir -p capacity-comparison/regression
task_original="$RUNNER_TEMP/atlxpres-history-original"
task_snapshot="$RUNNER_TEMP/atlxpres-history-snapshot.dump"
mkdir -p "$task_original"
git archive HEAD | tar -x -C "$task_original"
git show b422273:internal/controllers/provider_conversation_controller.go > "$task_original/internal/controllers/provider_conversation_controller.go"
git rev-parse HEAD > capacity-comparison/candidate-commit.txt
git rev-parse b422273 > capacity-comparison/original-query-commit.txt
sha256sum "$task_original/internal/controllers/provider_conversation_controller.go" internal/controllers/provider_conversation_controller.go internal/controllers/provider_threads.go > capacity-comparison/source-sha256.txt
docker build --build-arg LAB_BUILD_HISTORY_TESTS=true -f deploy/capacity-lab/Dockerfile -t atlxpres-capacity-lab:local .
docker build --build-arg LAB_BUILD_HISTORY_TESTS=true -f "$task_original/deploy/capacity-lab/Dockerfile" -t atlxpres-capacity-lab:original "$task_original"
for variant in local original; do
  docker run --rm --network none atlxpres-capacity-lab:"$variant" sha256sum /app/api /app/lab /app/history-tests > "capacity-comparison/binaries-$variant.txt"
done
# The workload executable must be byte-for-byte identical for both API variants.
diff <(grep '/app/lab$' capacity-comparison/binaries-local.txt) <(grep '/app/lab$' capacity-comparison/binaries-original.txt)
docker image inspect postgres:16-alpine redis:7.2-alpine haproxy:3.0-alpine > capacity-comparison/dependency-images.json 2>/dev/null || true
dc up -d --wait --wait-timeout 120 postgres redis
dc run --rm --no-deps seed
dc run --rm --no-deps runner /app/lab comparison-prepare
dc exec -T postgres pg_dump -U lab -d capacity_lab -Fc --no-owner > "$task_snapshot"
sha256sum "$task_snapshot" > capacity-comparison/snapshot-sha256.txt
# Explicitly loopback-only tests in the disposable PostgreSQL network namespace.
docker run --rm --network container:atlxpres-capacity-lab-postgres-1 --cpus 1 --memory 512m \
  --workdir /app/internal/controllers --mount "type=bind,src=$PWD/capacity-comparison/regression,dst=/evidence" \
  -e SETTLEMENT_TEST_LOCAL=true -e APP_ENV=test \
  -e 'DATABASE_URL=postgres://lab:lab-disposable-only@127.0.0.1:5432/postgres?sslmode=disable' \
  -e CHAT_HISTORY_INVESTIGATE=true -e CHAT_HISTORY_EVIDENCE_DIR=/evidence \
  atlxpres-capacity-lab:local /app/history-tests \
  '-test.run=^Test(BusinessHistoryBoundedMergeMatchesOriginal|BusinessHistoryOldCursorGenericPlanMatchesOriginal|ChatConditionalReadsConcurrentClientsAndBackdatedMessages|ProductChatGroupedHistoryMigrationAndReceipts|ProviderChatLatestMessagesAndOlderPages|CommunityLocationPinsPrivateAndIdempotent|CommunityMessageReceiptsPrivacyAndAuthorization|ProductChatMigrationPermissionsAttachmentsAndRetries|ChatUnreadIndexOnLargeHistory|ProviderChatReadAcknowledgement|BuyerConversationListingLookupIsScoped|NotificationConversationLookupAndProviderReadOwnership|BusinessContactPolicyAndLegacyThreads)$' \
  -test.v=true -test.timeout=240s > capacity-comparison/regression/tests.log 2>&1

restore() {
  dc down --volumes --remove-orphans
  dc up -d --wait --wait-timeout 120 postgres redis
  dc exec -T postgres pg_restore -U lab -d capacity_lab --no-owner --exit-on-error < "$task_snapshot"
}
fingerprint() {
  dc exec -T postgres psql -U lab -d capacity_lab -X -At -v ON_ERROR_STOP=1 -c "SELECT json_build_object('actors',(SELECT md5(string_agg(row_to_json(a)::text,'' ORDER BY slot)) FROM capacity_lab_actors a),'messages',(SELECT count(*) FROM provider_conversation_messages),'shared_messages',(SELECT count(*) FROM chat_messages),'rooms',(SELECT count(*) FROM chat_rooms),'revision',(SELECT sum(history_revision) FROM provider_conversations));"
}
# First validate the additional observer/check paths at smoke size, then restore.
export LAB_API_IMAGE=atlxpres-capacity-lab:local LAB_PROFILE=smoke
restore
fingerprint > capacity-comparison/fixture-fingerprint.json
dc up -d --no-build --no-deps --wait --wait-timeout 180 api1 api2 gateway
dc run --rm --no-deps runner > capacity-comparison/observer-smoke.jsonl

export LAB_PROFILE=baseline
run=0
for variant in original local local original; do
  run=$((run+1))
  export LAB_API_IMAGE=atlxpres-capacity-lab:$variant
  output="capacity-comparison/run-$run-$variant"
  mkdir -p "$output"
  restore
  fingerprint > "$output/fixture-fingerprint.json"
  cmp capacity-comparison/fixture-fingerprint.json "$output/fixture-fingerprint.json"
  dc up -d --no-build --no-deps --wait --wait-timeout 180 api1 api2 gateway
  python3 deploy/capacity-lab/comparison_resources.py snapshot > "$output/cpu-before.json"
  python3 deploy/capacity-lab/comparison_resources.py sample > "$output/resources.jsonl" &
  sampler=$!
  set +e
  dc run --rm --no-deps runner > "$output/results.jsonl" 2> "$output/runner-stderr.log"
  result=$?
  set -e
  kill "$sampler" || true
  wait "$sampler" 2>/dev/null || true
  python3 deploy/capacity-lab/comparison_resources.py snapshot > "$output/cpu-after.json"
  dc logs --no-color > "$output/services.log"
  printf '%s\n' "$result" > "$output/exit-code.txt"
  if [[ $result != 0 ]]; then
    echo "Isolated comparison stopped on a correctness/service failure; retain evidence."
    exit "$result"
  fi
done
docker image inspect postgres:16-alpine redis:7.2-alpine haproxy:3.0-alpine > capacity-comparison/dependency-images.json
dc config --format json > capacity-comparison/lab-configuration.json
lscpu > capacity-comparison/runner-cpu.txt
free -h > capacity-comparison/runner-memory.txt
