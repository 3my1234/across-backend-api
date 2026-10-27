# Notification-worker investigation: not qualified, 27 October 2026

The isolated actual-worker experiment **failed the original HTTP latency gate**. It does not qualify sustained notification capacity, backlog drain, retry recovery or worker restart. No production action, real notification/payment, migration, security-limit change or 500-session test occurred. The earlier worker-off 30-minute HTTP pass remains valid for its original scope.

## Exact execution and observed failure

[Run 40566887039](https://github.com/3my1234/across-backend-api/actions/runs/40566887039) tested `81ec3d32a49b6204af7b57a5bc2e31937a1aa9dc` on `experiment/notification-workers`. Its harness/worker/middleware regressions and SMTP/push mock preflight passed. All 100 synthetic identities completed gradual login and token validation. The 1,800-second profile then aborted after approximately **2 seconds of measured activity**, as required by unchanged abort-on-failure gates.

| Operation | Completed latency samples | p50 ms | p95 ms | p99 ms |
|---|---:|---:|---:|---:|
|Inbox|19|490.89|625.50|771.65|
|Marketplace send|36|374.70|519.25|599.33|
|Marketplace history|108|124.86|270.82|300.53|

Global successful-operation p95 was 507.09 ms. The limit remained p95 <500 ms. These are **short initial-burst samples**, not stable capacity percentiles or a sustainable RPS measurement. There were 240 offered requests and 197 completed responses (127 HTTP 200, 63 HTTP 201 including 27 idempotency replays, 7 HTTP 204); remaining requests/iterations were interrupted by the gate. No completed response was an HTTP error. That does not turn the aborted run into a pass.

The API executable was unchanged: SHA-256 `16e17986f680b7c709e16f1a2065984057d4d3bd5e73c15cf6c9de0ca3158de7`. The lab executable was `7ffd4bd7ec36ff095c794931ee4755286fde453f510c2aaecfa7681c0d4823d5`. No `internal/` application code changed. Only lab entry points, observation, mocks and workflow were added.

## Message reconciliation after abort

There were 61 offered logical message keys, 36 observed acknowledgements and 51 committed marketplace messages. **All 36 acknowledged keys were stored exactly once**, with no missing/duplicate/unknown keys. Fifteen requests committed without the generator observing their acknowledgement before abort; ten offered requests did not commit. All 51 stored messages were subsequently readable by their intended recipients, across 51 API pages.

Normal-completion reconciliation correctly failed: the process did not complete and offer=ack=stored was not satisfied. The retained journal distinguishes interrupted outcomes rather than reporting them as lost acknowledged writes or hiding them as successes. No Hall messages or offers were reached in this short first-journey burst.

## Actual worker execution and distinct acknowledgement stages

A separately bounded lab process invoked the **actual `workers.Run` email, push and push_receipts loops and actual delivery services**. It did not run the complete `cmd/worker` production entry/health-monitor binary. SMTP used an in-process localhost STARTTLS server with an ephemeral trusted lab certificate; native push used a mock RoundTripper with no external dialer. Roles that could execute payments or unrelated jobs were not started. API inline workers stayed off.

The worker process limit was 1 CPU/512 MiB. Existing API/PostgreSQL/Redis/generator limits and the large-busy synthetic seed remained unchanged. Provider ledger records included attempt count, mock acceptance, transport acknowledgement and mocked receipt acknowledgement separately from email/push database status.

At the last retained sample (`2026-10-27T19:17:58.554836854Z`):

| Channel | Unique mock keys / attempts | Provider acceptances | Transport acknowledgements | Worker database state | Mock receipt acknowledgements |
|---|---:|---:|---:|---|---:|
|Email|35 / 35|35|35|35 sent|0 (not applicable)|
|Native push|52 / 52|52|52|52 sent|0|

These totals include 8 initial email jobs and 1 preflight push. They show healthy mock submission and subsequent completion of this small backlog, **not sustained worker throughput or device receipt**. Native receipts were not yet eligible under the unchanged 15-minute policy. The table is the last observer snapshot, not a separately executed final drain verification.

The run aborted before the scheduled provider outage, accepted-but-lost acknowledgement, worker SIGKILL/restart and five-minute lease-recovery phases. Their results are **untested**, not passed. There is no measured steady-state arrival/service-rate comparison or qualified drain time. The static receipt policy (100 records per 15-second poll after 15 minutes) remains a limit to investigate, not a measured capacity result.

## Database pressure and an important confounder

A Docker sample near the failed burst reported PostgreSQL at 203.40% of one core, approximately its two-core quota; quota-boundary/sample effects mean this is not precise instantaneous CPU. Worker CPU in that sample was 2.46%. Slow-statement logs (only statements >=100 ms) recorded inbox SQL executions up to 390.653 ms. Pool counters from before warmup through the final recovery sample rose by 409/446 empty acquisitions and roughly 13.48/13.42 cumulative seconds of empty-acquire waiting on API1/API2. These counter windows include warmup/recovery; there is insufficient measured-window sampling for a stable pool-wait distribution.

This supports database contention as a **lead**, not a proven sole root cause. The mock provider itself records per-delivery attempts/acceptances/acks through the same PostgreSQL pool/database, adding artificial database work. Its impact has not been separated from actual worker queries, cold-start behavior, inbox query planning or normal initial traffic concurrency. Claiming that production worker code alone caused the regression would be premature.

Before another qualification, separate the mock provider ledger/storage from the application database, preserve real worker behavior, and run matched worker-off/worker-on comparisons with the same data, resources and initial-arrival pattern. Inspect the existing inbox query and plans. Keep the same latency/error/correctness gates. Do not raise CPU, pools, batch sizes, password admission or test thresholds simply to get a pass.

## Evidence and release recommendation

The complete artifact, aborted-run metadata, failed-step log, raw timing stream, source/configuration/binary hashes, partial journal and all observer samples are retained under `qualification/notification-workers-20261027/`. Artifact upload and disposable container/network/volume cleanup succeeded. The folder initially labelled preflight was renamed to aborted to reflect the actual evidence; raw contents were preserved.

**Do not release or advance to 500 sessions on this result.** This experimental branch changes only the lab; it contains no production worker fix to deploy. Review this failed run and the proposed controlled investigation before another capacity claim. Mobile login integration has its own result and remaining physical-device checks.
