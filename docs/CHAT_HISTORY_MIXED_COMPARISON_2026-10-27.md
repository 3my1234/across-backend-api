# Mixed-workload chat-history comparison — 27 October 2026

## Decision

The optimized backend passed both repetitions of the approved isolated mixed-workload experiment. At the 100-pair stage, weighted successful throughput rose from **313.41 to 432.74 requests/second (+38.08%)**. The worst operation p95 fell from **476–612 ms to 286–308 ms**, and worst operation p99 from **720–788 ms to 399–402 ms**.

All **250,331 measured requests** succeeded. All **65,167 acknowledged measured writes** were independently located exactly once. No measured 429, 503, unexpected error or invalid response was recorded. Both replicas served every stage, and both optimized repetitions passed the 500 ms per-operation p95 and 1,500 ms p99 targets.

**Recommendation: proceed, after approval, to the isolated 100-independent-session qualification.** This is not approval to merge, deploy, raise production traffic, provision infrastructure or start that next test automatically. The experiment remains on an unmerged development branch.

The earlier 8.37× result was a single-endpoint Windows experiment. The **38% result here is the broader mixed-workload improvement at the highest tested stage**; it must not be described as an eightfold platform improvement or proof of thousands of simultaneous users.

## Exact source and run

- Accepted [GitHub run 40503036203](https://github.com/3my1234/across-backend-api/actions/runs/40503036203), comparison job `122185249070`, completed successfully in 24 minutes 33 seconds, including setup, correctness tests, smoke, traffic, artifact upload and cleanup.
- Candidate: branch `perf/business-chat-history`, exact tested commit `a5e94496f90435a2610c3c46592af3d0ea33e055`.
- Control: the exact original `provider_conversation_controller.go` from `b422273`, overlaid on the same instrumented source as the candidate. The query helper's membership semantics are unchanged. Common observer/runner code is present in both builds.
- Both workload executables had identical SHA-256 `e1a1651d5198a2e6ece72d67cbf1e942f304e39e04e274a6da928bd76cab9a3b`. The API executable hashes differ, as expected; all hashes are retained in `binaries-original.txt`, `binaries-local.txt` and `source-sha256.txt`.
- The application change is the bounded per-conversation ordered history scan before merging pages. Two additive private super-admin runtime counters expose acquisition count and actual empty-pool wait time for measurement. Authorization of that private endpoint is unchanged. No new production schema or index is needed.
- PR 83's laboratory fixture correction remains part of the experimental base and unmerged; resolve/review that dependency before any future release.

## Matched experimental conditions

The run used a public-repository standard GitHub Linux runner with four logical CPUs. Both API variants used the same PostgreSQL 16, Redis and HAProxy image digests and resource limits. Source, binaries, image digests, runner CPU/memory and Compose configuration are retained with the evidence.

| Component | CPU limit | Memory limit | Other bound |
| --- | ---: | ---: | --- |
| PostgreSQL | 2 CPUs | 1 GiB | Same restored database and planner-statistics preparation |
| Each of two APIs | 0.75 CPU | 512 MiB | Eight database connections; 64 admission slots |
| Generator | 1 CPU | 512 MiB | 20/50/100 pair workers; sequential actions and two-second think time |
| Redis | 0.25 CPU | 128 MiB | Fresh cache for each restored run |
| HAProxy | 0.25 CPU | 128 MiB | Same routes/health checks and request headers |

The application and database share this runner, connected through an internal Docker network. No ports are published, runtime egress is blocked and real delivery workers are not running. This differs from production's separate hosts/private network and does not measure mobile-carrier latency, media-byte throughput, device rendering or notification delivery.

One synthetic database snapshot was captured and restored before each repetition. Its SHA-256 was `3c4b8a9df513e076fab4ed1b3216a3bfde6a39f68d75707b9df2ec5555c0ac26`. Logical fingerprints matched before all four measured runs: 100 actor pairs, 100,000 business messages, 205,000 shared messages, 20,010 rooms, 1,000 products and 1,000 listings. Actual UUIDs, timestamps and messages came from that same snapshot. Each restored database received an explicit `ANALYZE` pass before warmup/measurement.

Run order was **original → optimized → optimized → original**, all on the same runner. Each repetition used the existing 20-, 50- and 100-pair stages with a 90-second scheduling window, followed by drain. Actual stage elapsed time, including the final think/request drain, is recorded and used for throughput. Workload rules and initial data were identical; the faster version naturally completed more actions and accumulated more writes during its equal scheduling windows.

The operations were catalogue browsing, service listings, Town Hall feed/create/offers, buyer/provider business history and sends, bargaining/shared chat sends, busy-room history/sends, and Support history/replies. A pair worker alternates between its customer/provider identities; it is **not** two independent concurrent phone sessions. At 20 pairs, think time limits throughput, so a large RPS increase there is neither expected nor observed.

## Every stage and repetition

“Worst p95/p99” means the highest successful-response percentile among the measured operations for that stage. It is not a pooled percentile across operations. Percentiles are shown separately for each repetition, never averaged into a fictitious aggregate percentile.

| Run | Variant | Pairs | Successful RPS | Worst p95 | Worst p99 | Latency gates |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| 1 | original | 20 | 89.11 | 145.12 ms | 354.38 ms | Pass |
| 1 | original | 50 | 211.63 | 280.70 ms | 376.99 ms | Pass |
| 1 | original | 100 | 308.66 | 476.21 ms | 720.02 ms | Pass |
| 2 | optimized | 20 | 89.43 | 169.86 ms | 284.20 ms | Pass |
| 2 | optimized | 50 | 224.33 | 133.80 ms | 177.25 ms | Pass |
| 2 | optimized | 100 | 426.36 | 307.71 ms | 402.04 ms | Pass |
| 3 | optimized | 20 | 88.96 | 149.18 ms | 281.70 ms | Pass |
| 3 | optimized | 50 | 224.15 | 113.23 ms | 173.86 ms | Pass |
| 3 | optimized | 100 | 439.13 | 286.18 ms | 398.81 ms | Pass |
| 4 | original | 20 | 89.03 | 183.64 ms | 318.42 ms | Pass |
| 4 | original | 50 | 209.51 | 231.49 ms | 387.23 ms | Pass |
| 4 | original | 100 | 318.16 | 612.17 ms | 787.50 ms | Fail |

The original 100-pair stage met the latency gates in one repetition and missed p95 in the other. The optimized version met both latency gates in both repetitions at every stage. All four runs preserved correctness and writes.

Do not compare these results directly with the older archived 226 requests/sec result: that was a different run/environment. The matched control in this experiment is 309–318 requests/sec, and the corresponding candidate is 426–439. Two repeats provide useful comparative evidence, not a statistical guarantee or long-duration soak qualification.

## Database execution, CPU and pool waiting

History-query execution comes from PostgreSQL `pg_stat_statements`, reset at each measured stage. These are server execution **means**, not query p95/p99; client-side per-operation p50/p95/p99 are reported separately. Execution time includes server waits and is not the same as CPU time. Only the 30 statements with highest accumulated execution time were exported; the business-history statement is present in every stage. Broad “other” query labels do not support a precise claim about every remaining SQL cost.

| Pairs | Original mean history execution, repeat range | Optimized mean history execution, repeat range |
| ---: | ---: | ---: |
| 20 | 15.38–15.52 ms | 2.52–2.69 ms |
| 50 | 27.75–33.38 ms | 2.35–2.48 ms |
| 100 | 80.28–84.18 ms | 1.85–3.96 ms |

The history-query saving also reduced competition for connections. At 100 pairs, true queued acquisition time across both API pools fell from **62.26–68.00 ms per successful request to 5.54–7.39 ms**. This uses `EmptyAcquireWaitTime`, rather than incorrectly treating all acquisition overhead as waiting. Canceled acquisition deltas and total acquisition counters are retained independently. No pool size was increased.

CPU samples below are the mean Docker CPU readings inside each 100-pair stage, with 16 valid samples per role/stage/repetition. **100% means one logical CPU**, not all of the host. They are samples, not continuous CPU integrals.

| Component | Original mean CPU, repeat range | Optimized mean CPU, repeat range |
| --- | ---: | ---: |
| postgres | 177.62–193.40% | 121.78–122.35% |
| api1 | 17.72–19.54% | 23.47–23.84% |
| api2 | 18.97–19.70% | 23.74–24.68% |
| runner | 17.61–18.58% | 21.39–23.24% |
| gateway | 4.07–4.11% | 4.76–5.03% |
| redis | 3.19–3.77% | 3.93–4.20% |

PostgreSQL moved away from its two-CPU ceiling while serving more traffic. API and generator CPU rose with the additional work but remained below their limits; no newly saturated API/generator was observed in this workload. Mean CPU headroom is not a guarantee that individual bursts cannot contend.

Whole-container cgroup CPU counters were also captured before and after each three-stage run. Across the full campaign, including warmup/observer work, PostgreSQL CPU per measured request was **5.10 ms original versus 2.75 ms optimized (46.14% less)**. Unlike the earlier Windows process measurement, this Linux cgroup measurement includes PostgreSQL background processes. The two CPU accounting methods must not be compared as though they were identical.

## Per-operation latency at the 100-pair stage

Every cell is **p50 / p95 / p99 in milliseconds**. The four columns preserve the actual repetitions; other stages' operation counts and latency distributions are in the raw JSON.

| Operation | Original run 1 | Original run 4 | Optimized run 2 | Optimized run 3 |
| --- | ---: | ---: | ---: | ---: |
| bargaining_send | 68.78 / 202.81 / 267.18 | 56.11 / 222.07 / 348.59 | 5.92 / 50.12 / 125.35 | 4.79 / 39.75 / 71.68 |
| busy_room_history | 95.34 / 284.45 / 372.15 | 75.00 / 247.29 / 360.84 | 4.61 / 86.13 / 250.00 | 3.72 / 70.57 / 267.14 |
| busy_room_send | 101.62 / 239.59 / 299.10 | 97.83 / 214.05 / 284.00 | 14.38 / 135.58 / 229.17 | 10.83 / 100.19 / 206.19 |
| buyer_history | 165.72 / 355.70 / 516.35 | 137.18 / 412.13 / 589.02 | 6.18 / 70.04 / 150.10 | 4.03 / 58.39 / 166.52 |
| buyer_send | 207.22 / 438.63 / 518.01 | 158.07 / 405.69 / 552.57 | 14.67 / 307.71 / 402.04 | 8.66 / 286.18 / 398.81 |
| catalogue | 14.79 / 121.45 / 330.52 | 10.45 / 85.14 / 281.54 | 1.26 / 31.15 / 176.75 | 1.09 / 20.38 / 89.16 |
| provider_history | 194.85 / 452.16 / 720.02 | 163.71 / 612.17 / 787.50 | 6.61 / 88.75 / 186.78 | 4.21 / 75.31 / 186.44 |
| provider_send | 161.15 / 476.21 / 576.15 | 120.91 / 381.71 / 637.49 | 11.94 / 180.29 / 278.72 | 7.21 / 136.23 / 187.70 |
| services | 12.18 / 101.05 / 525.17 | 10.22 / 104.84 / 316.23 | 1.08 / 25.75 / 95.00 | 0.91 / 16.75 / 109.90 |
| support_history | 85.44 / 260.49 / 431.11 | 62.01 / 333.26 / 516.76 | 2.04 / 62.40 / 144.71 | 1.70 / 55.09 / 130.78 |
| support_reply | 42.54 / 129.00 / 156.42 | 17.11 / 120.49 / 179.71 | 2.56 / 104.35 / 117.62 | 1.96 / 98.96 / 142.98 |
| town_hall_create | 44.40 / 120.77 / 164.08 | 40.60 / 104.26 / 120.77 | 4.63 / 95.79 / 109.56 | 3.79 / 99.99 / 127.18 |
| town_hall_feed | 82.05 / 215.21 / 310.05 | 70.48 / 232.60 / 336.20 | 7.62 / 70.50 / 146.05 | 6.36 / 67.20 / 159.34 |
| town_hall_offer | 114.58 / 218.16 / 282.05 | 102.85 / 213.97 / 296.84 | 23.14 / 204.11 / 271.52 | 15.50 / 172.33 / 251.02 |

The benefit extends to catalogue/services, Support and shared/Town Hall activity, consistent with reduced database pressure rather than hiding work or returning fast errors. The remaining slowest p95 operation is now **buyer-message sending** (286–308 ms); Town Hall offers are another residual tail (172–204 ms). These remain within the declared gates in the accepted experiment.

That identifies the next endpoints to profile, **not a confirmed new SQL defect**. Notification/entitlement/transaction work and short synchronized bursts can contribute. The current broad SQL groups and CPU samples are insufficient to assign the entire send tail to one function or to prove lock contention. Do not remove authorization, billing or notification behavior simply to improve this number.

## Correctness and data integrity

| Run | Measured requests | Acknowledged writes | Stored once |
| --- | ---: | ---: | --- |
| run-1-original | 56,181 | 14,636 | Yes |
| run-2-local | 68,100 | 17,700 | Yes |
| run-3-local | 69,189 | 17,998 | Yes |
| run-4-original | 56,861 | 14,833 | Yes |

Both the old aggregate count and the new unique-key reconciliation passed. Every acknowledged measured write's unique synthetic key was found once in its corresponding business/shared message, Support message or full Town Hall description. The full description is checked because the displayed Town Hall title is intentionally shortened to 80 characters. Preflight and replay-check writes are outside the measured-write prefix and are not misreported as workload acknowledgements.

Before every measured stage, real HTTP checks rejected another customer's history request, checked page ordering and cursor boundaries across three pages, and confirmed replay of the same message reference returned the same ID with exactly one stored message. Both API replicas served every measured stage. No 429/503 or unexpected/invalid response occurred among the 250,331 measured requests.

All **13 selected PostgreSQL 16/Linux regression tests** passed, covering:

- Complete grouped pagination equivalence, timestamp/UUID ties, aliases, excluded customers/threads and old-schema compatibility.
- Business contact policy and lookup/read ownership, conditional reads and read acknowledgements.
- Message receipts, attachment permissions/replays and private idempotent static location messages.
- The explicitly targeted very-old-cursor/generic-prepared-plan regression.

The generic-plan limitation remains: with a cursor far back in a 10,000-message history, both plans filtered 9,850 newer rows. The candidate returned the correct 51-row page, and its captured server execution sample was 1.626 ms versus 2.416 ms for the original. This is **correctness coverage and a single execution sample, not a fix for linear scanning of very old generic pages**. A cursor-specific statement shape remains a separately reviewable optimization.

## Evidence and setup corrections

The complete accepted artifact is retained in the shared workspace at `qualification/chat-history-mixed-20261027/accepted-40503036203/`, alongside the downloaded job log. It contains all four raw result/resource/log folders, exact fixture/binary/source fingerprints, the two generic-cursor plans, regression results, dependency digests and runner/configuration data. `comparison-summary.json` was generated offline; the raw reports were not overwritten. The analyzer's accounting tests passed, including checks that fast errors/bad p99 cannot pass, SQL means are weighted by calls, and reset counters cannot produce fictitious negative waits.

Two setup attempts were stopped and excluded. The first lacked explicit post-restore `ANALYZE`, and its artifact upload also exposed root-owned regression files. The second was stopped before completing measurements to correct that ownership. The accepted attempt includes both fixes: analyze after every restore, and run the test container as the host runner's UID/GID. Earlier job logs and the available second attempt's partial setup artifact are retained separately; the first attempt had no downloadable artifact. No canceled-attempt measurement contributes to this report. PostgreSQL 16's [pg_dump documentation](https://www.postgresql.org/docs/16/app-pgdump.html) explains why optimizer statistics must be regenerated after a restore.

No production load test, merge, deployment, restart, production migration, proxy/Redis/VPN/billing change, Android build or infrastructure purchase was performed. The workflow's final cleanup removed only its own disposable Docker lab. No new independent-session phase has been started.

## Next phase, subject to approval

Recommend the existing **100-independent-session** isolated qualification: a bounded three-minute stage, then sustained measurement if its gates pass, using realistic screen/lifecycle polling and distinct identities. Keep the same latency/error/integrity requirements and collect recipient-observed messaging latency separately from server response time.

This campaign still omits driver/GPS update/search load, concurrent media transfers and sustained real worker/vendor/device delivery. Those coverage gaps, high availability and restore/recovery objectives remain in the audit. Do not jump to 500/1,000/10,000 sessions, declare production launch capacity or buy servers based on this result. Report the next phase independently before any production release decision.
