# ATL Xpress: release reconciliation and launch work, 4 November 2026

## Decision and boundaries

An isolated release candidate now contains the optimized application source and has passed real PostgreSQL regressions. The 500-pair preparation rehearsal also completed with the real SSH relay. This is progress toward release, **not a passed 500-session application qualification**. No VPS was contacted, production changed, mobile merged, infrastructure purchased, or live application load generated during this work. Free disposable GitHub CI was used because Docker is unavailable on this Windows workstation.

## Correct release source

Ancestry verified: ordinary `backend` HEAD `eece164781f5092dffa554f608eab6e0daf3a079` is an ancestor of GitHub main `30b0b68409f5dc12e9f63f332815d35acb1d3f08`, which is an ancestor of optimized image source `13d316c62ce8bdd35c1c2fe72bf3d4d357581914`. The earlier investigation's suggestion that the ordinary checkout contained newer divergent security work was incorrect. Preserve its uncommitted `internal/middleware/client_ip_test.go` and `.tmp/` nevertheless.

Candidate: `.worktrees/release-reconciliation`, branch `experiment/release-reconciliation`, verified commit **`7af6f5ba0b4dc1b7902448168dd8cf07a714597a`**. Built from current main, applying only the exact `internal/` and `migrations/` delta from the image source. No orchestration or qualification entrypoints were imported. API/worker entrypoints already match. Compared with image source, the only application-tree difference is the new test-only `internal/controllers/poll_cost_test.go`.

| Included optimization | Affected runtime files | Evidence/origin |
|---|---|---|
| Bounded, indexed chat history and merged provider threads | `internal/controllers/provider_conversation_controller.go`, `provider_threads.go` | `3238a9e`; result equality and bounded history regression |
| Bounded community inbox | `internal/controllers/community_controller.go` | `9b7c348`; legacy result equality and plan regression |
| Reduced repeated nearby-driver eligibility work | `internal/controllers/driver_availability.go` | `1f83501`; retained comparison evidence, exact image-source inclusion |
| Transaction-preserving bounded notification insertion | `internal/controllers/community_controller.go` | `eb9ffd1` through `d5a854c`; atomicity, recipient, rollback, boundary/concurrent retry regressions |
| Receipt claims, recovery and fencing | `internal/services/push_notifications.go`, `push_receipts.go`, `internal/workers/workers.go`, `migrations/108_push_receipt_claims.sql` | `19249e2`; concurrent claims, crash recovery, bounded requests and eligibility regressions |
| Pool diagnostic counters | `internal/routes/routes.go` | Exact image-source delta; does not increase pool limits |

Candidate commits: `2582ba4` reconciles source and adds disposable verification; `d6fb565` adds actual five-second list measurement; `7af6f5b` fixes the synthetic free-tier fixture without bypassing entitlement rules. All six targeted Go packages passed locally. Opt-in SQL tests were not credited as local execution: real PostgreSQL verification ran separately in CI.

**Release verification:** [41871729928](https://github.com/3my1234/across-backend-api/actions/runs/41871729928) succeeded at candidate `7af6f5b`. Six packages ran against disposable PostgreSQL; the workflow requires explicit PASS for six critical database tests, then all 16 polling measurement records and overall measurement PASS. It verifies fanout atomicity/deduplication/rollback, bounded history and inbox equality, and receipt claim/recovery/fencing. No production deployment is implied.

Safest release sequence: review this candidate against main; recheck main has not advanced before merging; preserve any intervening changes rather than resetting branches. Build immutable API/worker images from the reviewed merge SHA. Separately review migration108 against the actual deployed schema, index-build/lock effects, backup/restore evidence, settings and rollback. Apply migration and deploy only after explicit production approval. Migration is additive; do not drop receipt data or indexes as an automatic application rollback. Current production image/version/schema has not been reverified this turn.

## Preparation and monitoring corrections

Historical run41113053851: database watchdog stopped at19:12:07.409UTC with peer age6.485228 seconds, beyond unchanged5-second lease; PostgreSQL57P01 followed at19:12:08.558UTC. Production health and OOM checks were good at that stop. Main peer age was7.749636 seconds. The initiating coordinator exception was not retained, so its exact network/SSH/observer cause cannot be recovered or claimed fixed retrospectively.

| Confirmed correction | Files in qualification branch | Verification and limit |
|---|---|---|
| Seed rehearsal queried nonexistent `schema_migrations.version`; actual field is `name` | `deploy/cohost-lab/integration_seed.py` | Actual migration108 and500 actor pairs checked after seed, not fabricated fixture success |
| Valid HTTP observation and Docker inspection ran serially, risking status freshness failure even when each completed within its own bound | `watchdog.py`, `test_parallel_checks.py` | Concurrent inspection and three HTTP probes; barrier test proves concurrency; failed inspect still fails. Heartbeat published only after completed successful evaluation |
| Seed disconnect obscured initiating relay error | `trial_runner.py` | Original phase/status/age/sequence/error retained to stderr and `peer-relay-failure.json`; tests preserve original error |
| No long preparation replay of actual restricted SSH coordinator | `ci_peer_replay.py`, `integration_seed.py`, workflow | Actual `Runner.lab` and `Runner.leases`, pinned loopback SSH, actual seed executable and real PostgreSQL; host sensor observations explicitly simulated |
| Disposable loopback roles shared an SSH control socket because their endpoint/user/port matched | `ci_peer_replay.py` | Separate short role-specific ControlPaths. This is a rehearsal defect, not proven cause on distinct VPS IPs |
| Current CI Docker archive format differs from old flat OCI image format | `image_identity.py`, `test_image_identity.py`, `integration_image_identity.py` | Content-hash-checked bounded nested index traversal; skip only explicitly foreign platforms; require one reviewed platform leaf and validate loaded immutable identity/platform. Separate disposable safety CI must pass before any live proposal |

Local Python safety suite: **84 tests ran:83 passed and one Linux-only test skipped on Windows**. Root ownership, source integrity, CPU/memory caps, pool/admission limits, public failure rules and shutdown protections remain. HTTP2 seconds, total observer2.5 seconds, status3 seconds and peer lease5 seconds remain unchanged. No background publication of unverified healthy status.

**Successful seed proof:** disposable-seed-replay job in [41871463380](https://github.com/3my1234/across-backend-api/actions/runs/41871463380), source `848ba8e065785cd20add41031a44145dd7d1c6b8`: actual500-pair large-busy seed with expanded journeys completed in **253.325 seconds**, migration108 present, correct disposable identity, no OOM. Real relay remained active throughout. Injected missing database heartbeat then produced stale-peer rejection and five-second test-only database stop. Cleanup completed with no errors. Test PG3CPU/3GiB; seed1CPU/512MiB. No customer traffic or production host access. The overall run failed its separate image-identity compatibility check; do not label the entire run passed. Subsequent safety CI references are appended below once complete.

## Polling cost and decision

Read actual mobile timers: marketplace private messages/conversation list5 seconds with foreground/no-overlap guards; Town Hall feed10 seconds; community room messages20 seconds; product chat15 seconds; nearby drivers/location30 seconds. They are not all five-second polling.

Actual handler/PostgreSQL experiment in successful release run41871729928:20 sequential requests per ABBA arm; fixtures have50 conversations with100 messages each, private hot thread10000 messages, grouped private history, Town Hall buyer room1000 messages. Middleware supplies synthetic identity: this excludes JWT/proxy/TLS/device/media cost. SQL wall time is not database CPU. Not paced polling or a concurrent capacity test.

| Handler | SQL per request: full / unchanged | Observed p95 across measured arms |
|---|---|---|
| Five-second marketplace conversation list |2 / no conditional fast path|22.72–27.73ms, full response each time |
| Hot private history |2 /1|Full1.84–2.49ms; unchanged0.45–0.56ms |
| Grouped private history |2 /1|Full4.49–5.39ms; unchanged0.61–0.70ms |
| Town Hall buyer messages |3 /2|Full7.30–32.53ms; unchanged0.47–0.52ms |

Existing ETag fast paths materially reduce work; preserve them. Unchanged responses still query revisions, and Town Hall membership remains checked. Provider Town Hall entitlement cost was not measured in this buyer fixture. Conversation-list polling is the next **measured candidate** for targeted revision/ETag work; current21–23ms median is not proof of overload. Before changing it, preserve authorization, unread state, grouped threads, listing/entitlement visibility, last-message ordering, pagination, and invalidation correctness. No speculative cache or WebSocket rewrite was made.

Arithmetic only:500 screens polling every5 seconds offer100 requests/second. Private unchanged history would add roughly100 revision queries/second; list refresh roughly200 queries/second; if both operate simultaneously their costs add.5000 screens would multiply these by10. These figures are not supported-capacity claims and exclude other application traffic.

## Actionable remaining work

| Category and action | Files/scope | Expected impact | Verification | Safe without production? |
|---|---|---|---|---|
| Prelaunch: review reconciled release source | Candidate eight Go runtime files and migration108 above | Avoid releasing older unoptimized checkout | Exact main/candidate diff, immutable image source digest, passed regression run | Yes for review/build; merge/deploy require approval |
| Completed offline: disposable safety CI after OCI correction | `deploy/cohost-lab/*`, `.github/workflows/cohost-preparation.yml` | Close preparation compatibility blockers before asking operator to run anything | Full Linux ownership/watchdog/cleanup/transport/archive and seed replay passed together in41872754153 | Yes; no live setup authorized |
| Prelaunch: release/schema/settings/rollback reconciliation | `migrations/108_push_receipt_claims.sql`, deployment manifests and release checklist | Ensure API and worker use compatible schema and secure settings | Read-only deployed image/schema mapping and reviewed migration/rollback procedure; do not infer current state from October audit | Offline preparation yes; production action requires approval |
| Prelaunch: store/recovery/alert acceptance | Mobile release checklist, backup/monitoring runbooks | Defensible rollout rather than relying solely on load test | Retain physical-device/store build acceptance, independent restore rehearsal and actual alert delivery evidence; local archive readability is insufficient | Offline/disposable yes; production/store publishing requires approval |
| Safe now: targeted polling experiment | `internal/controllers/poll_cost_test.go`; mobile timer reads only | Established where repeated read work occurs | Successful16-record realSQL measurement, then realistic concurrent mix | Completed without production changes |
| Safe follow-up: evaluate conditional conversation-list refresh | `provider_conversation_controller.go`, `provider_threads.go`, `mobile/components/MarketplaceScreen.tsx` | Reduce full response/repeated list computation if revision lookup is cheaper | Matched before/after, all invalidation/access/unread/order cases; reject change without gain | Isolated implementation/testing yes; not implemented or release blocker by itself |
| Capacity: unchanged20→100→500 qualification | Existing staged scripts and exact reviewed caps | Establish actual sustained mixed-session limit | Qualified generator, endpoint latency/errors, DB/host metrics, acknowledged-write and retry reconciliation, queue drain | Requires separate explicit live authorization; NOT run this turn |
| Capacity: concurrent authentication and real delivery/media | Authentication, notification workers, actual mobile image paths | Cover gaps not established by preauthenticated/mocked tests | Separate burst correctness/resource test and device/network measurements | Disposable portions yes; real external/production activity separately scoped |
| Later: horizontal scaling/K3s, Redis GEO, database HA | Preserved `experiment/k3s-preparation`; infrastructure plans | Growth/resilience after measured need | Multi-instance correctness, DB headroom, restore/failover rehearsals | Preparation only; no purchases/install/migration now |

Earlier expanded100-session run40636465564 remains the largest passed application qualification:30 minutes,90152 requests, zero errors,14372 acknowledged keys stored exactly once, expanded bidding/location/nearby journeys and mock notification workers. It does not establish present production capacity. Independent500-session generator qualification is a separate result. Actual500-session application qualification and a numeric production traffic ceiling remain unproven.

No fresh host health/cleanup claim: latest retained audit is30 October19:21:57/19:22:07UTC for closed e859dc77e7e58c25, zero test containers/keys and healthy production at that time. This turn created no VPS resources and needs no user password/audit command. Do not rerun `-ApproveTrial` from this report.


## Final verified checkpoint
[Combined disposable validation41872754153](https://github.com/3my1234/across-backend-api/actions/runs/41872754153) **SUCCESS**, source **`836b8d671da0190bc094c2fadb5b6c0f0de394e8`**. Both offline-and-disposable-safety and disposable-seed-replay succeeded; live qualification was SKIPPED. Linux84 unit tests passed, real Docker archive import resolved the hash-verified platform parent index, real watchdog stop paths and ownership-restricted cleanup passed, and restricted SSH/PostgreSQL transport passed. Full500-pair expanded seed completed in **265.365 seconds** with the real pinned SSH relay, no OOM, correct migration108/identity/counts. Injected heartbeat fault rejected and test-only shutdown/cleanup passed; cleanup_errors empty. This closes the identified disposable preparation blockers; cross-VPS network behavior and actual500-session application capacity still require separately authorized execution.

Evidence saved under `qualification/launch-readiness-20261104/`, four complete job logs and SHA256SUMS.json. Release candidate remains7af6f5b, release verification41871729928SUCCESS. No new live test package was staged; no user command required.


## Subsequent final safety review
Candidate advanced to90b9056 with test-only migration verification, actualPostgreSQLCI41875176028SUCCESS. See RELEASE_AND_TRIAL_SAFETY_REVIEW_2026-11-04.md for migration/worker rollback conditions and concrete fresh local approval-only trial package. Production unchanged; live trial awaits explicit authorization.
