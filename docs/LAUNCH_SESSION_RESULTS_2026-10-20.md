# Launch qualification evidence: 20 October 2026

## Decision

The isolated four-session smoke and its recovery checks pass. **12,000 concurrent
active sessions are not qualified, and this is not a public-launch capacity sign-off.**
Continue controlled internal testing while resolving phone media/notification
reliability and completing representative staging qualification. No production
load test, paid infrastructure creation, store build or traffic migration was run.

## Reproduce and inspect

Tested source: `06d01846a94452929c3d7c03fdbe38ce95dfbfd5`, based on backend main
`bd28d0db341aaa8c5753dff9a78ea5dab885d2ab`. Portal/mobile sources inspected were
`f5cb302` and `07dafd6`. Deployment of those application fixes is not established
by this lab. Lab changes themselves do not require a production redeploy.

- [Passing session run 39424379470](https://github.com/3my1234/across-backend-api/actions/runs/39424379470)
- [Existing Go capacity lab 39424379414](https://github.com/3my1234/across-backend-api/actions/runs/39424379414)
- [Build and provider-policy checks 39424379380](https://github.com/3my1234/across-backend-api/actions/runs/39424379380)
- PR: https://github.com/3my1234/across-backend-api/pull/60

Session artifact `session-smoke-39424379470` contains summary, raw k6 time series,
resource samples, lifecycle, reconciliation, queue recovery and failover logs.
Retention is 14 days. Synthetic credentials and session fixture files are excluded.
See `deploy/session-lab/README.md` for repeatable local commands and safeguards.

## Exact executed envelope

Four distinct simultaneous customer/provider VUs, 45-second constant-VU window,
3–12 second thinking delays, up to 15-second graceful stop. Observed k6 duration
was about 53 seconds including completed iterations. Seed: ten customer/provider
pairs, busy Town Hall fixture, synthetic verified accounts. Initial mixed weights:
40% browse, 30% chat, 20% Town Hall, 5% Support, 5% auxiliary admin reads. Not every
weighted operation was necessarily selected in this short random run; absent
operation samples are not coverage.

Generator and containers shared a four-logical-CPU GitHub runner, AMD EPYC 9V45.
Each API was limited to 0.75 CPU/512 MiB and eight PostgreSQL connections;
PostgreSQL 1 CPU/1 GiB; Redis 0.25 CPU/128 MiB; HAProxy 0.25 CPU/128 MiB; k6
1 CPU/512 MiB. HTTP traversed internal Docker networking, without internet/TLS,
mobile networks, S3 or browser rendering. These are smoke limits, not production
hardware equivalence or an external-generator test.

## Results

| Measurement | Observed |
|---|---:|
| Started sessions / maximum observed VUs | 4 / 4 |
| Session actions / HTTP requests | 27 / 89 |
| Unexpected failures / controlled overload | 0 / 0 |
| Content assertions | 114 passed, 0 failed |
| Successful API latency p95 / p99 | 16.38 ms / 80.65 ms |
| Confirmed writes / stored writes | 14 / 14 |
| Marketplace / Town Hall messages | 12 / 2 |
| Duplicate marketplace message bodies | 0 |
| Messages found through intended peer's HTTP history after run | 12 / 12 |
| Incoming messages observed during the workload | 10 / 12 |
| Delivery/read receipt rows for generated messages | 10 |
| Native push jobs recovered and submitted to mock | 14 / 14 |
| Duplicate/failed native push jobs | 0 / 0 |

Actual signup, unverified-login rejection, synthetic-token email verification,
login and persisted profile update passed for customer, product merchant, service
professional and mixed provider. Provider onboarding/profile reads passed for all
three provider types. No real verification email was dispatched.

All 14 push jobs became durable retry jobs after the mock transport returned 503.
The test then simulated stale `sending` leases and invoked the actual dispatcher.
All were submitted successfully to the in-memory mock in about 16.55 ms, with no
duplicates. This is a tiny mechanics check, not worker drain capacity, an actual
worker-process restart, Expo acceptance or phone notification evidence. Browser
push had zero jobs and is not covered by this run.

One disposable API was stopped after the workload. The check encountered a
transition timeout, then obtained three consecutive readiness responses explicitly
from api2 within 11 seconds of beginning probes after stop completion. The restored
api1 then passed readiness. This proves this lab's bounded convergence and restart,
**not zero interruption, failover under load, main-host survival or database failover**.
Dependencies requiring external configuration intentionally show false in lab
readiness; staging-mode core readiness passes without claiming those features work.

### Chat latency limitation

Observed incoming-message polling latency was p95 31.82 s, p99 36.26 s, maximum
37.37 s. The mixed VUs only poll when their randomly selected chat activity runs;
this is not a continuous foreground-chat timer or a production/device measurement.
The server responded quickly, but these results **cannot certify chat timeliness**.
The next workload must schedule active-chat polling independently of browsing,
retain encoded send times and declare end-to-end delivery targets before testing.
Post-run visibility verifies completeness, not timely device display.

### Failed checks retained in the audit trail

- Run 39408407700 stopped during setup: the harness incorrectly required JSON
  from an expected plaintext 401. Corrected to accept the actual expected error
  contract; authentication still must return 401. Latency gates were unchanged.
- Runs 39416439760 and 39421513020 passed session/write checks but failed the
  new recovery step. The test initially assumed three-second convergence and
  later treated a first success as stable. Compose restoration also attempted to
  rerun the single-use seed. Fixed to require three consecutive survivor responses
  within a bounded polling loop and restart exactly the stopped API container.
  These failures were not relabelled as successful capacity tests.

## Production observation supplied by operator

At `2026-10-20T13:39:07+02:00`, public readiness returned HTTP 200 in 0.830478 s;
loopback replica-gateway readiness also returned `ok:true`, pool maximum eight,
all listed dependency checks true. This contradicts a blanket outage claim based
only on workspace connection/DNS timeouts. It does not explain yesterday's upload
failure or establish present phone behavior, all deployed revisions or throughput.

Reported host memory use was 14%, disk 10.9%, load average 3.26 on four CPUs.
Docker's snapshot showed Coolify about 58% CPU and its PostgreSQL about 39%; Docker
CPU percentages can exceed 100% on multicore machines. No memory exhaustion is
demonstrated. Peak contention, shared-host scheduling, database wait and network
failures during the incident remain unproven. Do not buy one server per portal
or infer user capacity from these snapshots.

## Remaining gates and concrete order

1. Confirm application/worker/PWA deployed revisions. Retest affected Samsung
   foreground/background/locked phone notifications and photo uploads across
   hotspot and independent networks; correlate request IDs and upload stages.
2. Add isolated real object transfer, thumbnail-size measurements, browser-push
   transport and full worker-process fault/drain tests. Complete lifecycle writes,
   sandbox subscription callbacks and declared independent chat polling workload.
   Product checkout is deliberately disabled and must not be invented as coverage.
3. Deploy an isolated production-like app/database split and a separate generator,
   after the exact regional quotes, access, resource inventory and cleanup plan
   are reviewed. The US$25/eight-hour document is a **proposal**, not approval or
   verified provider pricing. No paid execution is authorized by this result.
4. Execute 100 → 500 → 1,000 → 2,500 → 5,000 → 10,000 → 12,000 stages only while
   prior correctness, latency, worker and resource gates pass. Then run 30-minute
   sustained, two-hour soak, independently scheduled spike/reconnect, cold/warm,
   dependency/host-loss and backup-restore checks. None have been executed here.
5. Size expansion from the first observed limiting resource. Current copies share
   one host; a second application host and independent routing are required for
   application-host loss, with explicit Redis/database recovery. Copies alone do
   not provide autoscaling, an unconstrained connection budget or millions capacity.

See `LAUNCH_ARCHITECTURE_AND_COVERAGE.md`, `LAUNCH_RELIABILITY_GATE.md` and
`STAGING_TEST_COST_PROPOSAL.md`. The read-only next-stage tool reviews session
software gates only; it still requires separate resource, recovery, media and
cost review. It neither starts the next stage nor signs off a public launch.
