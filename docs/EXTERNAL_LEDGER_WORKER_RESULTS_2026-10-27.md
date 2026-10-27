# External-ledger worker investigation - 27 October 2026

Status: the four matched short stages and the 30-minute HTTP/fault/send-recovery stage passed. Receipt confirmation capacity did not keep up: 11,311 eligible receipts remained waiting. Stop before 500 sessions and review the proposed receipt investigation. No production action occurred.

## Reproducible source and isolation

- Comparison run 40587689346 tested `edb5b92d3b33686cf61bd4a133e5a6f9d89dd12a` on `experiment/notification-workers`. API binary SHA256 `16e17986f680b7c709e16f1a2065984057d4d3bd5e73c15cf6c9de0ca3158de7`, unchanged from the prior HTTP qualification. Lab binary `d81dd0d192c8f6e0b017a61c90c1660af8625293146bdb7195503c35d0ecca82`.
- One identical PostgreSQL snapshot was restored before each stage: SHA256 `342ff26a6a5432ae241aab1d398d685ab6ed195c7a88076632195130f6966f3d`. Each restore is ANALYZEd because pg_dump does not retain planner statistics. Redis is reset and APIs restarted identically. The order is OFF/ON/ON/OFF; fixed per-VU seeds reproduce journey choices/think times. This is a closed-loop workload: completion timing can cause a small difference in the number of actions completed. Baseline row timestamps are identical, so their wall-clock ages increase across stages, without changing the pending-job eligibility used here.
- 100 distinct synthetic sessions perform real, gradual logins and token checks before each 180-second active stage. Think times remain 3-12 seconds. Original 500 ms p95,1500 ms p99, error/correctness, polling and reconciliation gates are unchanged.
- Existing 4-vCPU GitHub runner only. PostgreSQL 2 CPUs/1 GiB; APIs 0.75 CPU/512 MiB each, pool 8; generator 1 CPU/512 MiB; worker 1 CPU/512 MiB, lab pool 4; mock provider 0.25 CPU/128 MiB. Internal Docker network, no published ports, production secrets, production data or runtime external notifications.

## What changed in the test machinery

The mock provider now runs in its own bounded process with an append/fsync file journal and in-memory key counters. It has no PostgreSQL connection. Actual email/push/push-receipt workers.Run loops and delivery services communicate with this provider over SMTP/TLS and a fixed internal HTTP adapter. The adapter rejects non-Expo targets. This qualifies the actual loops through a lab wrapper, not the full production worker entrypoint or every worker role.

A distinct lab_worker database role separates worker SQL statistics from API/observer SQL. Provider CPU and journal duration are separate from worker CPU. The file journal still shares the runner disk and is not a zero-cost mock; outside-DB instrumentation removes the prior per-delivery SQL contention, not all instrumentation effects.

## Matched comparison results

| Stage | Completed workload requests | Request-window RPS | Inbox p95/p99 ms | PG CPU mean/peak % of one core | Acknowledged writes |
|---|---:|---:|---:|---:|---:|
|1-off|8,460|46.22|147.43/262.45|16.38/27.14|1,232|
|2-on|8,460|46.22|147.19/281.53|17.63/34.07|1,231|
|3-on|8,464|46.24|123.75/232.37|16.46/38.76|1,232|
|4-off|8,462|46.23|98.24/226.29|17.34/32.99|1,232|

All 33,846 offered workload requests completed. All 4,927 acknowledged writes were stored once, with no missing, duplicate, unknown or unconfirmed committed keys. Intended peers could retrieve every new marketplace message. The original unauthorized/expired-session warmup checks, idempotent replays, ordering and polling assertions passed. There were no HTTP errors or dropped iterations.

The request window is approximately 183 seconds including graceful completion. k6?s offered rate includes authentication setup and is approximately 38 RPS; it must not be confused with the 46 RPS active request-window rate. Neither is maximum platform throughput.

Inbox is the slowest p95 operation in all four stages. Overall successful-request p95 is 22.45-27.69 ms; the operation-specific inbox tails are shown above rather than hidden in the aggregate.

## Notifications and queues

The ON stages finished with 423/424 emails and 3,283/3,284 native pushes marked sent by the worker. Provider acceptance keys, generated ACK counts and application completion counts agreed; no application sent row lacked a corresponding mock ACK. Sampled send-backlog peaks were 25 emails and 100/92 pushes; sampled pending/retry/sending ages stayed below 3.55 seconds. Both send backlogs were empty by the final check. OFF-stage pending rows intentionally accumulate and are not failed delivery.

Provider ACK counters mean a mock generated its response before the network write. Application sent state separately records worker-observed successful processing and durable completion. Neither is actual recipient delivery. The short stages do not exercise the real 15-minute receipt delay; the separate sustained run below does.

Worker CPU averaged 0.88-0.90% of one core, mock-provider CPU 1.83-2.01%, and generator CPU approximately 5-6%. Captured top80 SQL statements attributed approximately 1.08-1.11 seconds of execution to lab_worker versus 33.8-37.2 seconds to API/observer lab queries over setup, measurement and recovery. These are aggregate execution times, not database CPU time or a complete all-statements inventory.

## Inbox and the original startup spike

The prior two-second failure (run 40566887039) remains retained as failed evidence and is not directly comparable to a complete 3-minute distribution. Its mock wrote several SQL operations per notification in the same database. The corrected, unchanged-API comparison no longer reproduces its latency-gate failure, and ON values overlap the OFF range. This strongly supports test-instrumentation contention as a significant confounder; it is not proof that every earlier slow request came from that one cause.

Startup still has a smaller cold/concurrent-arrival tail even with workers OFF: the first OFF stage includes a 510.23 ms maximum inbox request, while its p95 passes at 147.43 ms. Subsequent maxima are 346.92-397.22 ms. First-two-second inbox samples are separately retained. Captured inbox SQL mean execution is 15.7-19.1 ms, maxima 188.5-249.6 ms. After-load EXPLAIN ANALYZE repeats take about 9.1-16.1 ms across forced custom and generic plans. Those plans have warmed buffers; they are not cold-cache or very-old-page proof.

API pool empty-acquire waits are present but small in aggregate: first OFF 0.794/0.939 seconds across the two pools, first ON 0.724/0.647 seconds, second ON 0.145/0.137 seconds, final OFF 0.169/0.217 seconds. No cancelled acquires. No lock waiters were seen in roughly 5-second snapshots. Sampling can miss sub-second CPU/lock spikes, so cold planning, concurrent start and short waits cannot be separated conclusively here.

## Historical harness failures retained

-40586058154: setup stopped when Compose reran the seed dependency against a restored database; artifact upload also hit a synthetic-journal permission error. No workload capacity result. Fixed no-deps restore startup and artifact readability.
-40586954542: preparation was cancelled before measurement after noticing that restored fixtures needed fresh planner statistics. Not a performance failure.
-40587689346: all four corrected stages, artifact retention and cleanup passed.

## Sustained fault/recovery result

[Run 40591221683](https://github.com/3my1234/across-backend-api/actions/runs/40591221683) tested `66d9567602ad21dd9f0b4aaa483b59310aa8a205`, after independently verifying all four comparison proofs, unchanged application/session sources, and identical API/lab binary hashes. Its original 100-session 1800-second profile, software gates, final reconciliation, artifact retention and cleanup passed. VU telemetry records 1,800 samples at 100 active sessions, spanning approximately 1,800.000 seconds.

- 81,341 offered workload requests completed, zero HTTP errors/overload/content failures/dropped iterations; 45.10 RPS over the 1803.52-second first-offer/last-completion window. k6 reports 44.17 offered RPS including authentication setup.
- 12,018 OFFER=ACK=stored message keys, exactly once: 7,305 marketplace and 4,713 Town Hall messages. No missing, duplicate, unknown, unconfirmed or unpersisted offered keys. All 7,305 marketplace messages were visible to intended peers over 200 pages. Idempotent replay, authorization, order and polling assertions passed. No Town Hall offers were generated by this session mix.
- Worst operation services p95 35.45 ms/p99 44.42 ms. Inbox p95 17.06 ms/p99 22.67 ms, maximum 53.39 ms. Overall p95 13.42 ms/p99 28.50 ms. This separately seeded/warmed sustained run is not a matched cold-start comparison with the restarted-API ABBA stages.
- Sampled PostgreSQL CPU mean 16.46%, peak 70.89% of one core, under the two-core quota. APIs mean 3.44/3.42%; worker 0.65%; mock provider 2.04%; generator 5.58%, peak 19.0%. API pool waits increased only 4/5 ms across one empty acquire per API; zero cancelled acquires. No sampled lock waiters. Five-second sampling does not rule out shorter spikes.
- A 25-second SMTP 451/push 503 outage produced 45 email retries and 437 push retries at the 20-second observation. After a 40-second provider-delay injection, the actual worker was killed with 89 owned sending jobs (8 email, 81 push). At approximately 30 seconds after restart, 0 had recovered; at approximately 330 seconds, all 89 had recovered. Original five-minute lease timing was retained.
- Sampled send-backlog peaks 58 emails/508 native pushes. Oldest sampled sending/retry job ages approximately 300/303 seconds, consistent with the injected lease wait. After workload and the 60-second readiness recovery, the send backlog was already empty at the bounded drain check; that check itself took one second. This is not a claim that an outage backlog drained in one second.

| Channel | Provider unique keys | Acceptance events | Generated ACK events | Worker durable completions | Mock receipt confirmations |
|---|---:|---:|---:|---:|---:|
| Email |3,702|3,707|3,706|3,702|Not applicable|
| Native push |32,755|32,812|32,755|32,755|6,501|

The intentionally lost acknowledgements caused repeated provider acceptance for 5 email keys and 57 push keys. These are at-least-once retry effects, not duplicate stored business messages. The mock uses deterministic tickets; real vendor deduplication/ticket behavior and handset delivery remain unqualified. Provider per-key attempt counters omit a request interrupted at the control-delay gate, so they are not a complete count of every wire attempt. Generated ACK counters do not prove the client received that packet; worker durable state is reported separately. No completed application delivery lacked its mock ACK evidence.

## Receipt bottleneck and proposed next step

At final verification there were 6,501 mock-receipt-confirmed rows and 26,254 sent rows awaiting confirmation. **11,311 of those were already old enough to be checked**, while the remaining 14,943 had not yet reached the minimum 15-minute age. This is a measured receipt-confirmation backlog, distinct from the empty submission/send queues. The provider journal puts the oldest approximately 25.3 minutes after its generated send ACK (about 10.3 minutes beyond eligibility), an approximation rather than an exact database sent_at age.

The unchanged code requests at most 100 receipts per 15-second tick after the 15-minute age guard: a theoretical ceiling of 6.67 checks/second per loop. Approximately 32,755 pushes were submitted in this 30-minute experiment, around 18.2/second of active time. Confirmation cannot keep pace at that arrival rate with one such loop, even though sending and API latency remain healthy.

Do not simply add parallel receipt workers: the current receipt SELECT does not claim/lease its rows, so concurrent loops can poll the same records. Prepare a separate experimental proposal for bounded backlog draining with safe coordination, unchanged receipt age, vendor limits, timeout/restart handling and idempotent completion. Compare it under the same workload before recommending deployment. Do not change password hashing/admission limits, production settings or purchase resources to mask this finding.

## Recommendation and remaining limits

Accept this as an isolated 100-session HTTP, submission and worker-recovery milestone. **Do not proceed to 500 yet.** Review a receipt-processing optimization proposal first, then rerun its sustained/failure qualification with unchanged gates. No application/internal implementation was changed in this worker investigation; only isolated lab machinery and reports changed.

These results exclude actual email/push vendors and recipient delivery, web push (zero fixture rows), full production worker entrypoint/other roles, driver GPS load, concurrent media transfers, production hardware/network, very old history-page capacity and physical mobile behavior. They establish neither millions of users nor a production concurrency limit. The original failed evidence remains retained.

Local evidence: qualification/external-ledger-workers-20261027/, with raw artifacts and metadata for both successful runs and the historical setup/cancellation attempts, `comparison-analysis.json`, `fault-analysis.json` and reproducible analyzers. The identical snapshot hash proves matched comparison inputs within the CI run; its private dump was not exported, so a fresh reproduction generates new synthetic UUIDs/timestamps with the same scenario/cardinalities.

The analyzers and compact results are also retained in `docs/capacity-results/2026-10-27-external-ledger/` for review.
