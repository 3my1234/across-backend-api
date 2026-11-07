# Core-feature launch audit — 2026-11-06

This checks release source and the current mobile checkout, not the deployed
production image or store binaries. External reviewers' traffic estimates are
hypotheses, not a measured safe user limit.

| Area | Confirmed implementation | Remaining risk |
| --- | --- | --- |
| Town Hall | Ten active providers per room, enforced by handler and database admission trigger; room lock serializes closure/membership/writes; retries and notification writes stay transactional. | Hot-room lock and pool waits remain measured problems. A fifty-admitted-provider room is not the current product contract. Do not remove locking or silently raise the cap. |
| Drivers | Mobile foreground heartbeat normally every 30 seconds; Android minimized location requests a 30-second interval. Publication is serialized and suppresses duplicates within 13 seconds. Server rejects writes inside ten seconds and expires presence after 120 seconds. | Location publication writes PostgreSQL, not Redis. Phone scheduling is not guaranteed. Delayed/failed updates must expire; steady-state throughput is unqualified. |
| Nearby discovery | PostgreSQL GiST point index and bounding-box filtering before precise distance/eligibility checks; at most 25 results; five-second handler context deadline. | A spatial index is not proof that the full query or concurrent presence updates meet latency targets. |
| Mobile chats | Shared conversation/inbox polling every 20 seconds while app active; product chat every 15 seconds; Town Hall discovery/pending-detail refresh every ten seconds. No WebSocket rewrite proposed. | These are checkout-specific intervals, not every client or deployed binary. Many simultaneous polling clients still generate load. |
| Mobile retries | Driver loop avoids overlapping requests; transient failure delays increase 10/20/30 seconds and cap at 30; authorization/access failure stops availability. Provider request helper does not blindly retry every write. | Driver retry is not exponential with jitter. Do not claim a complete mobile retry-storm audit from these helpers alone. |
| Media | Provider media and chat photos request signed upload URLs and PUT file bytes directly to storage. Signed uploads omit application credentials. | API still signs/validates/completes media, and device compression/video completion cost are not qualified by a JSON load test. |
| Overload | API request admission defaults to 128 in-flight per process with retryable 503; shared Redis-backed auth/community rate limits exist. | Effective production settings and Cloudflare rules were not queried. Application pool code applies explicit SQL timeouts to the migration pool; this does not establish runtime Town Hall lock timeout settings. |

Source references:

- `internal/controllers/community_town_hall_controller.go`, `community_controller.go`,
  `migrations/080_shared_chat_town_hall.sql`, `internal/routes/routes.go`.
- `internal/controllers/driver_availability.go`, `migrations/089_driver_availability.sql`.
- `internal/middleware/admission.go`, `internal/db/db.go`.
- Mobile `components/providerDriverPresence.ts`, `driverLocationTask.ts`,
  `driverPresenceSession.ts`, `CommunityScreen.tsx`, `ProductSellerChat.tsx`,
  `CommunityTownHall.tsx`, `nativeProviderApi.ts`, `NativeProviderCatalog.tsx`,
  `chatImages.ts`.

Existing mobile driver-session and background-location tests passed when run
from the mobile project directory. They verify lease/account scoping, sign-out,
serialization, stale location rejection and foreground service behavior with
mocks. They do not establish behavior of a released Android/iOS binary on devices.

## Mixed checkpoint

Dedicated test branch `experiment/launch-mixed-checkpoint`, source `1572457`, uses
the existing qualified generator bytes and original stage policy at 20 sessions
for 180 seconds. API is pinned to `395ef241b9a35be9712cb6fc7d107bfe8be85f38`,
whose PostgreSQL 16 regression passed in `42203503855`; both running API executable
hashes must match the built artifact. Full 500-pair, large-busy fixture, real
PostgreSQL 16/Redis, ordinary chats, hot Town Hall activity, presence/offline and
nearby search remain in the existing mixed workload. No fixture reduction or
latency/error threshold relaxation. Gateway captures X-Cache; per-operation k6
results, resource/lock samples and independent write reconciliation are retained.

Run: https://github.com/3my1234/across-backend-api/actions/runs/42210541636

### Completed result: PASS

The complete original 20-session, 180-second stage passed on PostgreSQL 16,
Redis and two API processes through the gateway. All declared thresholds passed;
20 sessions were authenticated and no iterations were interrupted. Source
`1572457` is the test harness; the actual API source is `395ef24`. Both running
processes and the downloaded executable match SHA-256
`c8aa65b6d9618a4a8698616b0370d045d94dd8d1077041051e4ebeb502863c63`.

| Measured operation | Client p95 |
| --- | --- |
| All successful measured requests | 37.86 ms |
| Catalogue, ordinary cache hit/miss mix | 76.97 ms |
| Service search | 45.05 ms |
| Town Hall offer | 13.53 ms |
| Town Hall send | 9.87 ms |
| Private marketplace send | 12.39 ms |
| Inbox | 12.80 ms |
| Driver presence update | 6.62 ms |
| Nearby search | 8.66 ms |

Overall successful-request p99 was 69.85 ms. There were 1,912 measured requests
offered, 1,956 HTTP requests including setup, zero unexpected errors and zero
controlled overload responses. The configured 180-second constant-user stage
completed; total k6 wall time also includes setup and graceful completion.

Independent inventory: all 319 offered and acknowledged writes stored exactly
once, zero missing/duplicate/unknown/unconfirmed committed keys. Current bid
states and final presence states reconciled. There were 60 durable bid messages,
94 Town Hall messages and 165 private marketplace messages; 120 offer HTTP calls
targeted the same Town Hall room (including intentional retry requests), so
hot-room traffic was retained. Only two providers exercised presence: 11 online
updates and two offline transitions. This does not test hundreds of drivers.

Gateway catalogue evidence: 148 cache hits and 42 misses across the six original
query variants (77.9% hits). Gateway-total p95 was 2 ms on hits and 85 ms on misses.
Those are gateway timings, distinct from the client p95 table above. No cache TTL,
authentication, payload, or acceptance threshold was relaxed.

Twenty-nine resource observations sampled no lock waiters; short waits between
samples are not excluded. Peak sampled PostgreSQL container CPU was 19.12% of one
core and memory 251.7 MiB. App-group peak memory was 108,994,560 bytes; DB-group
peak 2,048,147,456 bytes includes page cache and differs from Docker's displayed
working set. OOM counters remained zero. Sampling and quotas on a shared GitHub
runner do not prove equivalent VPS capacity or independent CPU allocation.

Cleanup completed successfully. The small queue retry/recovery preflight passed.
Mixed-run reconciliation verifies persisted notification/delivery rows, not
worker drain or external notification delivery: external email/push/device and
payment journeys remain outside this load test.

Raw evidence: shared workspace `qualification/launch-mixed-42210541636/`, workflow
log/status, `RESULT_LAUNCH_MIXED_42210541636.json` and SHA-256 inventory. Compact
results are also committed alongside this report as
`LAUNCH_MIXED_RESULT_42210541636.json`.

This is a free disposable GitHub runner, not either VPS. Resource quotas do not
guarantee independent CPU cores on the runner. Even a pass is not 500-session
VPS qualification. It is bounded release-candidate evidence to decide the next
action. No production deployment, migration, SSH password or purchase is involved.

## Release decision requirements

This is a successful bounded current-candidate checkpoint, not broad-launch
sign-off. The justified capacity action is one separately approved isolated VPS
checkpoint with this exact verified binary; further speculative SQL changes are
not justified by this passing mixed run. The old VPS package must not be rerun:
its API pin predates these catalogue corrections. 100/500 sustained VPS capacity
remains unqualified. Do not label a failed performance gate as data corruption,
or passing acknowledged writes as a full capacity pass.

Current billing/payment lifecycle, SES production sending readiness/quotas, actual
device push delivery, store binary identity, backup restoration and deployment
rollback still require their own existing release evidence. This audit did not
access the live accounts or verify these gates. Pacing announcements can reduce
a synchronized burst but does not prove any numerical user capacity.
