# ATL Xpress: established-session and authentication results, 27 October 2026

**The 100-established-session HTTP stage passed. Authentication burst investigations completed, but most simultaneous first attempts were rejected. No 500-session test, sustained/soak run, merge, deployment or production change occurred.**

## Exact sources and scope

| Experiment | Exact source | Successful run |
|---|---|---|
|100 established sessions|`e9e34a1ac6773b0500efd80cb856205074c58d36`|[40540592038](https://github.com/3my1234/across-backend-api/actions/runs/40540592038)|
|20/50/100 login bursts|`833b3c440748059da5467b5f1cc243f98388af2b`|[40544727615](https://github.com/3my1234/across-backend-api/actions/runs/40544727615)|
|20/50/100 registration bursts|`833b3c440748059da5467b5f1cc243f98388af2b`|[40544923309](https://github.com/3my1234/across-backend-api/actions/runs/40544923309)|

All three used the **same API executable**, SHA-256 `16e17986f680b7c709e16f1a2065984057d4d3bd5e73c15cf6c9de0ca3158de7`, also identical to the previously successful mixed-workload optimized candidate. Application code under `internal/` is unchanged from `a5e94496f90435a2610c3c46592af3d0ea33e055`. Only lab harness, observation and accounting changed. Sources remain on experimental branch `perf/business-chat-history`; nothing was merged.

Lab executable hashes: established-session `60d16483522fe12a43de491799fba8a16c3f563b84abe1f320900696ff31ad44`; final login/registration `cd6d60bf92a335913e7fec53f9cede983e897c7a9d2fbaed67663ee3162d53c1`.

Existing public standard GitHub runners only. Each experiment had its own disposable PostgreSQL 16/Redis/two-API/HAProxy lab with no public ports or runtime external egress. PostgreSQL 2 CPUs/1 GiB, each API 0.75 CPU/512 MiB/pool 8, generator 1 CPU/512 MiB, Redis and gateway 0.25 CPU/128 MiB each. No resources were purchased. These are laboratory limits, not a fresh production inventory.

Large-busy fixtures: 100 buyer/provider pairs, 1,000 products, 1,000 services, 100,000 marketplace messages, 205,000 shared-chat messages and 20,010 rooms. 100 distinct identities were selected for the established-session test. Synthetic credentials, tokens and verification secrets were excluded from artifacts. Raw measurement timestamps are UTC.

## Phase A: 100 established sessions

Warmup performed 100 real password logins sequentially, pausing 250 ms after each. Every token was rechecked after the final login before measurement. All 100 passed; login p50/p95/p99 were 67.63/72.99/77.63 ms, with zero 429/503. Warmup used its own metrics/tags and is excluded from workload latency/rate calculations.

The measured scenario held 100 active VUs for 180 seconds. Gauge evidence contains 180 samples at 100 and approximately 179.99989 seconds of sampled dwell at the full target; subsequent samples 93/79/64 are graceful drain. The three-minute scenario used 3-12 second think times and independent 15-second conversation polling. Existing activity assumptions were 40% browsing, 30% chat, 20% Town Hall, 5% Support and 5% admin; every first journey sent a message and replayed its idempotency key.

- **8,373 offered=8,373 completed workload requests**, all expected 2xx; zero unexpected HTTP errors, 429/503 or failed content/identity assertions.
- 2,436 completed journeys. Measured HTTP response window 182.917 seconds including drain: 45.77 requests/sec. This is a closed-loop human-style workload, not the earlier pair benchmark or a maximum-RPS test. Whole-test 38.49 RPS includes warmup and must not replace this workload rate.
- Zero reported dropped iterations. There was no independent arrival-rate schedule, so no claim about serving an offered open-loop spike.
- Both APIs served traffic. All declared HTTP gates passed unchanged: per-operation successful p95<500 ms/p99<1,500 ms, unexpected errors<0.5%, zero normal overload responses and zero content/authorization failures.
- Worst per-operation p95 was 55.14 ms; worst p99 was 70.50 ms. Inbox p95 was 25.66 ms over 767 samples, rather than the earlier invalid run's three-sample 542 ms signal. This does not erase that old observation or prove performance under other workloads.

| Successful operation | Samples | p50 ms | p95 ms | p99 ms |
|---|---:|---:|---:|---:|
| admin_overview | 129 | 1.84 | 4.18 | 6.47 |
| catalogue | 952 | 1.52 | 26.52 | 33.01 |
| hall_history | 465 | 2.32 | 4.55 | 6.32 |
| hall_send | 465 | 6.76 | 11.18 | 15.16 |
| inbox | 767 | 14.97 | 25.66 | 45.44 |
| market_history | 2052 | 3.01 | 5.25 | 14.49 |
| market_retry | 100 | 4.69 | 20.39 | 65.79 |
| market_send | 767 | 5.44 | 11.27 | 25.86 |
| product_detail | 485 | 3.86 | 6.77 | 9.87 |
| receipts | 649 | 2.19 | 4.48 | 9.38 |
| service_detail | 467 | 3.30 | 5.85 | 7.24 |
| services | 952 | 0.83 | 55.14 | 70.50 |
| session | 59 | 3.99 | 5.16 | 27.08 |
| support_history | 64 | 1.72 | 3.32 | 5.29 |

### Write integrity and message observation

**1,232 logical offers=1,232 acknowledgements=1,232 stored keys, each exactly once:**767 marketplace messages and 465 Town Hall messages. Missing, duplicated, unknown, unconfirmed-committed and offered-but-not-stored keys were all zero. All 100 idempotent replays returned the same message IDs. Post-run recipient paging found all 767 marketplace messages with exact bodies visible to their intended peers, across 100 history pages.

Polling observations had p95 14.237 seconds and p99 14.834 seconds, within the predeclared 17/20 second gates. These are observations by the synthetic 15-second poller, not push/phone-render latency or a promise of instant delivery. Full recipient completeness was checked separately from this observed-message latency distribution.

The corrected journal records each logical offer before HTTP and each observed acknowledgement after the response. It checks acknowledged keys independently even after aborts, reports uncertain commits separately, and refuses to qualify an aborted/incomplete stage. Eight negative/abort cases plus parser scoping passed local and CI tests. Chronological page ordering regressions and policy checks passed. Earlier invalid/aborted evidence remains retained; no acceptance threshold was relaxed.

### Resource evidence during activity

Docker CPU percentages below use 100%=one CPU; PostgreSQL's allowance is 200%, each API 75%, generator 100%. These are sampled means/peaks during the request window, not production headroom or a linear projection to larger user counts.

| Container | Samples | Mean CPU | Peak CPU | Last sampled memory |
|---|---:|---:|---:|---|
| atlxpres-capacity-lab-sessions-run-28924 eef 3954 | 23 | 6.85% | 14.23% | 91.03 MiB / 512 MiB |
| atlxpres-capacity-lab-gateway-1 | 23 | 0.96% | 1.73% | 10.05 MiB / 128 MiB |
| atlxpres-capacity-lab-api 1-1 | 23 | 4.56% | 8.37% | 36.99 MiB / 512 MiB |
| atlxpres-capacity-lab-api 2-1 | 23 | 4.53% | 8.48% | 37.84 MiB / 512 MiB |
| atlxpres-capacity-lab-postgres-1 | 23 | 18.22% | 46.72% | 252 MiB / 1 GiB |
| atlxpres-capacity-lab-redis-1 | 23 | 1.91% | 3.62% | 9.438 MiB / 128 MiB |

Each API had 36 pool samples, max sampled acquired 1 of 8, no new empty acquisitions, canceled acquisitions or empty-acquisition wait-counter increase between measurement snapshots. Sampling can miss short peaks. Redis sampled peak logical memory 2,763,616 bytes, zero evictions/rejected connections and peak 318 instantaneous operations/sec. Generator CPU/memory did not approach its cap.

Worker processes were deliberately absent. End-of-run queue counts included 387 email outbox rows and 3,275 native push delivery rows; no drain/queue-age sustainability claim is made. This means HTTP-stage success is **not** full-platform launch qualification against every worker/delivery criterion in the broader audit.

## Phase B: initial authentication bursts versus later retries

Each cohort started distinct synthetic clients nearly together, using the existing trusted generator address and distinct forwarded client IPs. The first 20 fresh clients each reported 19 remaining out of the unchanged 20/minute auth IP limit, confirming this case did not collapse them into a single source IP. Shared-carrier/NAT behavior and production edge trust remain separate untested cases.

The password bulkhead remained **two active operations per API**, four across the two-API lab. It bounds concurrency, not the total number of sessions. More than four requests may succeed across a short burst as slots free; five initial successes in the 100-login wave does not violate that bound.

| Initial simultaneous starts | Login success /503 | Signup accepted /503 | Login eventually successful under retry model | Login time-to-success p95 /maximum |
|---|---:|---:|---:|---:|
| 20 | 4 / 16 | 4 / 16 | 20 / 20 | 3.19 / 3.22 s |
| 50 | 4 / 46 | 4 / 46 | 50 / 50 | 5.93 / 6.02 s |
| 100 | 5 / 95 | 4 / 96 | 100 / 100 | 8.70 / 11.29 s |

The successful workflow conclusions mean the investigation/accounting/recovery checks completed, **not** that zero-rejection authentication capacity passed. The initial login waves rejected 80%,92% and 95%; signup rejected 80%,92% and 96%. No 429 or unexpected response replaced these controlled 503 observations.

### Login: current one-attempt behavior versus hypothetical retry model

The first-wave outcomes model a single HTTP attempt. For 503 only, a separately labeled client model allowed at most six total attempts, waiting the actual`Retry-After:2`seconds plus deterministic 0-1 second jitter. Every recorded retry respected that wait. The model ultimately signed in all 20/50/100 clients. It made 16/69/198 additional attempts, including 0/23/103 additional 503 responses; those rejections remain in the evidence.

| Login cohort | Accepted first-wave p50 ms | Accepted first-wave p95 ms | Rejected first-wave p95 ms | Extra retry attempts | Extra 503 s | Client-start spread ms |
|---|---:|---:|---:|---:|---:|---:|
| 20 | 159.70 | 194.10 | 46.91 | 16 | 0 | 0.43 |
| 50 | 171.26 | 208.87 | 90.42 | 69 | 23 | 7.54 |
| 100 | 122.14 | 237.23 | 121.24 | 198 | 103 | 2.40 |

Accepted-first-wave latency sample counts are only 4/4/5; their percentiles are descriptive, not sustainable authentication SLOs. Client-start spread is generator dispatch timing, not exact backend arrival spacing. Eventual success is measured over each entire modeled journey, including waits.

**The released mobile source does not implement this model.** At local mobile revision 3 fd 8 df 7, password login makes one POST and shows an error on failure; it does not automatically honor Retry-After or implement bounded jittered login retries. History-read retries are a different path. No mobile code, APK, stored-password behavior or production service was changed here.

### Registration: independent mutation and outbox checks

Registration used new synthetic emails in each cohort and made **one request per account, without automatic retries**. Each cohort accepted 4 accounts; the 16/46/96 rejected requests created no accounts. All 12 accepted cohort accounts plus three separate recovery-control accounts were stored once, inactive/unverified, with a verification token and exactly one pending, unattempted verification-email record each. All 15 accounts retained the actual default bcrypt cost 10; hashing was not weakened. All six cohort/control reconciliation reports passed.

| Signup cohort | Accepted p50 ms | Accepted p95 ms | Rejected p95 ms | Client-start spread ms |
|---|---:|---:|---:|---:|
| 20 | 158.81 | 174.88 | 138.82 | 1.48 |
| 50 | 196.23 | 212.31 | 110.17 | 2.96 |
| 100 | 174.67 | 213.19 | 118.06 | 11.42 |

These checks cover account creation and the durable verification outbox, not SMTP/vendor delivery, inbox placement, provider onboarding approval or how long real users wait for verification. No real email was sent and no registration mutation was blindly replayed. Current user accounts, subscriptions and payment data were not involved.

## Is the login issue a conservative limit or CPU saturation?

**The direct 503 cause is the fixed password admission guard; CPU quotas also constrained admitted password work.** Before/after admission rejection-counter deltas equal every recorded 503, including retry rejections, with the limit unchanged at 2 on both APIs. For example, the 100-login cohort recorded 95 initial +103 retry rejections and admission deltas 96+102=198.

Cgroup CPU intervals overlapping the 100-login journey averaged 35.78%/35.49% of one CPU on the APIs, with short interval peaks 80.86%/80.44% and 41/34 throttled periods. Each API has a 75% CPU quota; short sampled intervals can cross quota-period boundaries. The averages include deliberate retry waits; they do not show sustained 100% utilization. PostgreSQL averaged 8.82% and the generator 8.16%, with no CPU throttling observed for either in that cohort.

Registration's 100-user wave also recorded API CPU-quota throttling, one/two periods. Its API overlapping-interval means were 58.80%/67.64%; very short bursts have only one or a few cgroup intervals, so these include adjacent work/idle time and are not precise instantaneous peaks. The 200 msadmission sampler can miss the active part of a sub-second wave; a sampled active count 0 does not mean the guard was unused.

Thus raising the two-operation limit alone is not a demonstrated solution. The guard deliberately rejects before queuing more password work; admitted hashing can also exhaust its small CPU allowance briefly. A later change needs measured latency, fairness, CPU and recovery evidence. No security, quota, hashing, Redis or proxy settings were changed to make results look better.

## Recovery and cleanup

All three experiments recorded 12 readiness probes over the following minute, all 200/`ok:true`: established 2.191-5.026 ms, login 2.012-8.359 ms, signup 2.545-10.847 ms internally. Each login cohort's separate single-login recovery control returned 200; each signup recovery control returned 202 and passed account/outbox invariants. Artifact upload and disposable-container/data cleanup succeeded for all final runs.

## Preserved provenance and limitations

A first login setup 40542285627 was canceled before measurement to move the investigator from a dynamically addressed exporter to the **already trusted** static lab runner. Trust policy itself was not expanded. Its metadata/log/artifact are retained.

Partial login run 40543021403 is also retained and excluded from the final comparison. Its 20 cohort completed;50 client journeys completed under the model, but the observer failed before its final admission/recovery snapshot, and 100 was not attempted. The 20 msprobe rate could exceed the 600/minute API budget; the old generic observer error did not retain the failed response status, so rate-budget exhaustion is an inference from configuration and observed request volume. The final 833 b 3 c 4 source reduced probe frequency to 200 msand reports status on future observer failures. No workload/security threshold was changed.

These results use one execution per final scenario on a shared CI host, synthetic cached browsing terms, fixed fixtures and HTTP polling. They do not qualify 500 sessions, millions of users, 30-minute sustained operation, long soak, media bandwidth, driver/GPS updates, real notification workers/vendor delivery, full email verification delivery, payments, production Internet/TLS latency, carrier NAT fairness or signup/login/reconnect arrival-rate campaigns. Prior generic very-old-page scan limitations remain. The earlier matched 38% mixed-workload improvement remains separate evidence.

Raw evidence directory:`qualification/session-qualification-20261027/`, with full artifacts, job logs, source/binary hashes, configuration, metrics, every synthetic offer/acknowledgement, account/outbox outcomes, cgroup CPU and admission counters, runner metadata, recovery and cleanup. Offline analyzers and SHA-256 manifests are retained alongside a portable evidence ZIP. No private fixture/session files are published.

## Recommendation and stop point

1. Keep the 100-user three-minute HTTP-stage pass as a bounded milestone; complete the audit's 100-user 30-minute sustained test before treating it as an operating target.
2. Prioritize a reviewed client experience for temporary authentication overload: clear feedback and bounded Retry-After-aware login handling, independently tested on devices. The experimental jitter model is evidence to inform that change, not shipped functionality.
3. Keep signup retries separate and mutation-safe; qualify real verification delivery, legitimate shared-IP traffic, account-targeted abuse and reopen/session-expiry paths independently.
4. Use CPU and admission measurements to evaluate any later queue/concurrency/API-resource change. Do not weaken password hashing, remove abuse protections or purchase hardware based solely on account totals.
5. Do not start 500 sessions or merge/deploy from these results. Present findings and obtain the next phase approval.
