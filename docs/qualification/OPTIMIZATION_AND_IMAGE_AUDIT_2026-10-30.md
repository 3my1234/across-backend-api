# Optimization and exact-image audit — 30 October 2026

No production change or new capacity pass is asserted. This audit distinguishes
implemented code, isolated validation and deployed production state.

## Exact application being tested

The cached application images were built from
`13d316c62ce8bdd35c1c2fe72bf3d4d357581914`, bundle run40930181592.
The cached archive SHA256 is
`020bacefeffd12bdfa460c165b43b471f9f987b5da7579bdfd219137ceb2c366`.
Current qualification `internal/`, `migrations/`, `cmd/api/` and
`cmd/capacity-lab/` have an empty Git diff against that image source.
The live runner enforces that comparison. Coordinator-only fixes do not require
another application image upload. All commits below were verified ancestors of
the image source where an implementation commit is listed.

| Component | Implemented and in test image | Evidence and remaining limits |
|---|---|---|
| Business chat history | `3238a9e`: bounded per-conversation lateral reads before grouped pagination; existing cursor/order and authorization preserved. `internal/controllers/provider_threads.go`. | Matched handler comparison ~55→460 requests/sec, p95 ~740→80ms, query-specific. `CHAT_HISTORY_QUERY_INVESTIGATION_2026-10-27.md`; no whole-platform 8× claim. |
| Inbox and indexes | `9b7c348`: unread work limited to returned page; indexed latest physical-thread previews. | Differential authorization/pagination/schema regressions40634848191; expanded100 run40636465564 passed. Not integrated500 proof. |
| Nearby-driver queries | `1f83501`: distance-ordered candidates with lazy current entitlement/document checks, no arbitrary pre-limit. | Equality/revocation40741981937 and actual-handler40746907768 passed; ~1.33–1.86× query-only. No Redis GEO replacement. |
| Notification insertion | `d5a854c`:64-recipient INSERT SELECT batches within the existing business transaction. `community_controller.go`. | Rollback, recipient/privacy/blocking, concurrent retries, conflict-only pagination and final-page failure regressions passed. No-skip40854663425: SQL calls1100→100 per100 transactions,29–39% cumulative SQL-time reduction across comparisons. Full500 still unqualified. |
| Receipt workers | `19249e2`, migration108: durable claims/fencing, bounded draining, shared pacing/backoff; receipt HTTP outside SQL transaction. `internal/services/push_receipts.go`. | Real PostgreSQL recovery/race tests40626904902 and sustained10040627566223 passed. Fivefold scheduling improvement in small matched policy test; external handset delivery not proven. |
| Other background notifications | Existing durable send queues and leases retained; actual workers use mocked transports during qualification. | Expanded100 recovered110 interrupted sends;3703 emails and56745 native push records reached mock completion/receipt. Mock acknowledgement is not actual delivery; interrupted acknowledgement can permit duplicate external acceptance. Browser push subscription/delivery load not qualified. |
| Town Hall bids | Existing durable/idempotent business writes and authorization retained; notification batching shares transaction boundaries. No new separate bidding rewrite claimed. | Expanded100 reconciled2354 bid messages plus chat; all14372 acknowledged logical write keys stored once.500 burst/lock/fanout behavior pending. |
| Driver/GPS | Existing leased presence, offline transitions and private-coordinate protections retained. No new Redis position architecture claimed. | Expanded100:487 updates,120 offline transitions,3036 nearby searches; final-state/replay/privacy accounting passed. Physical-device GPS and500 scale pending. |
| Authentication | bcrypt cost/security and bounded admission retained. No claim the backend login burst bottleneck was solved by raising limits. Mobile bounded retries are a separate workstream. | Earlier100-person burst accepted only5 logins/4 registrations immediately; prototype retry reached100 over11.3s. Established sessions are not simultaneous login/registration capacity. Current APK/production acceptance is separate. |
| Database pools | Existing bounded pgx pools/configuration/admission retained; trial API pool8. No blind connection increase implemented. |100-session reports include pool observations. Shared database pressure and500 contention remain to measure. |
| Redis | Existing shared dependency retained; no Redis GEO or unmeasured cache rewrite introduced. | Isolated session tests exercised existing Redis; no independent Redis saturation/failover or production recovery qualification claimed. |
| Listings/products/services | Existing browse/search/detail paths retained; no unsupported blanket indexing/cache optimization claimed. | Expanded100 worst service-operation p95/p99 41.39/51.39ms. Media CDN/image-download/device-rendering performance is outside this HTTP lab workload. |
| Resource sizing | Gateway1CPU has targeted latency evidence versus0.25CPU; current cohost resources remain aggregate-capped. | CPU allocation measurements are isolated experiments, not application throughput proof. More RAM alone did not solve failed500 bursts. No purchased infrastructure. |

The proposed notification sender fast path was **rejected**, despite equality
tests, because matched measurements did not justify adoption. It is not an
outstanding required migration. No migration109 was introduced for it.

## What the100-session tests proved

Initial workers-off run40549418096, source6e486729:100 established sessions,
30minutes,80898 requests, zero HTTP errors,12125 acknowledged writes stored
once; worst-operation p95/p99 48.14/58.37ms. It did not qualify background
delivery, driver traffic, login bursts or production.

Later **expanded** run40636465564, source
`fd9fb42fa4477a32c5315f807dcc8099f132d19a`, included actual Town Hall bids,
GPS/nearby operations and actual notification workers with mock transports:
100sessions/1800seconds,90152 requests, zero HTTP errors/overloads/dropped
iterations,14372 acknowledged write keys stored once, no final send or
due-receipt backlog. Worst-operation p95/p99 41.39/51.39ms.
These are stronger isolated results, not a verified production traffic limit.
Retained reports: workspace qualification/EXPANDED_SUSTAINED_RESULTS_2026-10-28.md
and RECEIPT_CAPACITY_RESULTS_2026-10-28.md.

Earlier20/50/100 authentication bursts are distinct from established-session
qualification. This audit does not invent an additional50-session sustained pass.

## Production versus qualification

Repository inclusion and inclusion in the isolated image are verified above.
The latest audit confirms16 main-host and3 database-host production containers
healthy, zero resources/keys for closed run70005c7a6e37d3c6, and protected sites
healthy. It does **not** establish that production runs image13d316c or contains
experimental migration108. No production merge, migration or deployment was
performed by this work. Read-only deployed image/schema mapping is still needed
before claiming an optimization is live; production launch/recovery checks remain.

## Latest failure and correction

Run41108080378 failed before installing CI access or generating load:
historical generator artifact download returned HTTP503, "Egress is over the
account limit." Both server archive cleanup markers and the subsequent
independent current-host audit confirm cleanup. This is no application result.

The previously downloaded original gate.json and executed-sessions.js are now
retained in deploy/cohost-lab/proofs/generator-40866958782, verified against the
original SHA256SUMS.json. The checker anchors both hashes and preserves original
duration/resource/assertion checks. The workflow checks this proof without a
historical artifact download. Current generator headroom must still pass during
the actual application trial; historical proof never substitutes for it.

No500 or1000 sustained application pass exists. Planned unchanged trial:
20/45s →100/180s →gradual500/180s →simultaneous500/1800s, then recovery,
queue drain and original receipt eligibility tail. Failures stop progression.
Application p95<500ms, p99<1500ms, zero overloads, integrity, resource and
production-health requirements remain unchanged. No numeric safe launch limit
can be assigned from orchestration failures.
