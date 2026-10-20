# Isolated HTTP session qualification

This extends, rather than replaces, `deploy/capacity-lab`. k6 is pinned to 2.3.0,
verified against the official release and documentation on 20 October 2026.
The production app and infrastructure are never modified by these tests.

## Run the safe smoke

The **Isolated session smoke** GitHub workflow runs four distinct VU identities
for 45 seconds, plus setup and graceful stop. It builds real APIs, Redis,
PostgreSQL and HAProxy with no public ports on an internal Docker network.
Runtime egress is blocked; no real payments/email/push are sent. Existing Actions
minutes/allowances apply. It is a smoke-sized CI run, not a capacity target run.

Locally on a disposable machine with Docker Compose and Node:

```sh
node deploy/session-lab/test-policy.mjs
export LAB_SCENARIO=busy LAB_ACTOR_PAIRS=10 LAB_SESSION_TARGET=4
export LAB_SESSION_PROFILE=smoke
export LAB_EXCHANGE_DIR="$PWD/session-exchange"
mkdir -p session-exchange session-evidence
docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml up -d --build --wait gateway
docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml run --rm --no-deps exporter > session-exchange/fixtures.json
python3 - <<'PY'
import json
from pathlib import Path
data=json.loads(Path('session-exchange/fixtures.json').read_text())
data.pop('sessions')
Path('session-exchange/run.json').write_text(json.dumps(data))
PY
docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml run --rm --no-deps lifecycle > session-evidence/lifecycle.jsonl
docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml run --rm --no-deps sessions
# Always run this, including after k6 fails or aborts:
docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml run --rm --no-deps verifier
```

Exporter requires a fresh marked `capacity_lab` database. Calling preparation a
second time fails. No arbitrary origin/DB URL can be supplied. Exported fixture
and run files contain synthetic credentials: do not publish either. Artifacts
include only summary/timeseries, resource/lifecycle reports and synthetic server
logs. Docker cleanup below destroys only the named disposable lab.

```sh
docker compose -f deploy/capacity-lab/compose.yml -f deploy/session-lab/compose.yml down --volumes --remove-orphans
```

## Session model, assumptions and assertions

Each VU owns one distinct customer/provider identity, performs actual password
login, validates session binding and retains its session while thinking. Fixture
population supports up to 6,000 pairs (12,000 distinct identities); that is a seed
bound, **not an executed capacity result**. No user count is inferred from RPS.
The `vus` time series records simultaneous VUs; `session_starts` records identities
that actually started, and `session_actions` records activity. Paused thinking
sessions remain concurrent sessions. There are zero WebSocket connections.

Sessions think 3–12 seconds with startup jitter. Initial mixed weights are 40%
browse/search/details, 30% marketplace chat, 20% admitted Town Hall, 5% Support,
5% admin overview. `chat-heavy` uses 15/50/30/3/2. These are explicit assumptions,
not measured production proportions. Every VU now keeps its conversation polling
independently at 15-second intervals while thinking or browsing. HTTP operations
within a VU remain sequential, so a slow request can delay its timer; missed ticks
produce one current poll rather than a catch-up burst. This does not reproduce
every browser/native timer simultaneously. All seeded providers are
mixed; separate lifecycle checks cover product and service onboarding identities.

Every first iteration sends an idempotent marketplace message and verifies an
immediate replay returns the same message id. Recipients independently poll;
unique observed incoming messages and time from their encoded generator timestamp
to polling observation are measured. This is application polling latency, not a
native/browser push receipt or phone-render time. Read acknowledgements use real
receipt endpoints. Final DB counts must exactly match successful writes, including
after threshold failure. Missing evidence fails reconciliation.
The verifier then pages real recipient history until every generated marketplace
message is found with its exact stored body. This post-run completeness check is
separate from polling latency during load, and is bounded at 10,000 messages;
larger workloads need reviewed sharded verification rather than silent sampling.

## Predeclared gates

- Successful API latency per operation: p95 <500 ms; p99 <1,500 ms. These initial
  usability targets expose server delays while leaving a separate budget for
  internet/device/rendering time. Percentiles of failed responses cannot pass.
- Unexpected response/content failure <1%; every content/identity assertion must
  pass. Normal session stages permit no 429/503. Burst mode records controlled
  JSON overload with positive Retry-After separately; HTML/timeouts are failures.
- Both replicas must serve requests; planned VUs must actually start; no dropped
  iterations for an independently scheduled burst. Arrival-rate VUs are generator
  capacity, not a promised simultaneous-user count.
- Zero duplicated/missing confirmed database writes. Media/transaction state
  reconciliation is not yet available and is never implied by message counts.
- Incoming polling observation p95 <17 seconds and p99 <20 seconds, declared
  around the 15-second timer plus server/generator scheduling budget. These are
  polling-service gates, not an instant-message or phone-push promise.
- Aggregate gates abort unhealthy traffic early, and stage-specific evidence must
  pass before ramping further. Do not override thresholds after seeing a failure.

## Larger profiles: prepared, not executed or approved

`policy.mjs` defines `stage` (180s), `sustained` (30m), `soak` (2h), `stress` (5m),
`reconnect` (2m) and independent `burst` (90s). The ramp target sequence is 100,
500, 1,000, 2,500, 5,000, 10,000, 12,000. Run each step independently so cumulative
percentiles cannot hide a newly failing stage. Review its complete summary and
reconciliation before advancing. Current CI exposes smoke only.

Non-smoke profiles require `LAB_SESSION_APPROVAL` to name a mounted JSON file
with `authorized`, `environment: isolated-staging`, matching profile,
`max_sessions`, `max_duration_seconds`, `spending_cap_usd`, `approved_at` within
24 hours. A sample is deliberately **not authorized**:

```json
{"authorized":false,"environment":"isolated-staging","profile":"stage","max_sessions":100,"max_duration_seconds":180,"spending_cap_usd":0,"approved_at":"REPLACE_AFTER_REVIEW"}
```

This gate documents prior approval; it is not a cloud billing enforcement system.
No paid resources are provisioned by the suite. Existing Docker CPU/memory limits
are smoke defaults and are unsuitable for a 12,000-session generator. The suite
currently accepts only `http://gateway:8080` with the disposable response marker;
an independently deployed external generator path must be reviewed and enabled
before claiming production-like topology tests. Do not substitute production IPs.

## Outstanding qualification

Upload-heavy mode still fails immediately: the separate six-photo correctness
check below is not a scalable upload workload. Do not pretend presign-only
requests or six sequential transfers represent media capacity. Workers
are not started. A separate check exercises the real native-push batch dispatcher
through an in-memory HTTP transport: upstream 503, durable retry, stale sending
lease recovery and complete submission to the mock. It does not prove vendor,
device delivery, full worker-process restart or sustainable queue throughput.
CI also stops one disposable API, records bounded routing convergence, requires
three successes on the survivor and checks readiness after restarting the API.
Cold/warm comparison, concurrent media size mix, signup/search/upload spikes, fault
of dependencies, backup restore and payment sandbox callbacks remain explicit gaps.
See `docs/LAUNCH_ARCHITECTURE_AND_COVERAGE.md` for route-level coverage and
`docs/LAUNCH_RELIABILITY_GATE.md` for device and production launch requirements.

Official tool references:
- https://grafana.com/docs/k6/latest/using-k6/scenarios/executors/constant-vus/
- https://grafana.com/docs/k6/latest/using-k6/thresholds/
- https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/dropped-iterations/
- https://github.com/grafana/k6/releases/tag/v2.3.0

## Real-byte isolated photo correctness

The **Isolated photo roundtrip** workflow layers `media-compose.yml` on the lab.
It builds pinned upstream MinIO source at RELEASE.2025-10-15T17-29-55Z and creates
a one-day disposable TLS identity. Only the internal Docker network resolves the
synthetic bucket hostname to this server; there are no published ports or runtime
egress. All credentials and certificates are synthetic. Actual API configuration
and S3 signing code are exercised unchanged, with normal TLS verification.

`/app/lab media-check` creates only its fixed disposable bucket. It generates
valid JPEGs at 256, 1,024 and 2,048 pixels square and sends each in both directions:
presign, actual PUT, message attachment with HEAD validation, recipient history
and actual signed GET. Recipient hashes must equal upload hashes; message replay
must be idempotent. Incomplete uploads, wrong-owner keys and unsigned private
downloads must be rejected. Six confirmations must match six durable messages.
Signed links, fixtures and TLS private keys are excluded from artifacts.

This proves selected S3-compatible semantics and application roundtrips. It does
not establish AWS IAM/CORS correctness, mobile/browser transfer latency, concurrent
media capacity or background notification display. Source references:
https://github.com/minio/minio/tree/RELEASE.2025-10-15T17-29-55Z
https://github.com/minio/minio/blob/master/docs/config/README.md
https://github.com/minio/minio/blob/master/docs/tls/README.md
