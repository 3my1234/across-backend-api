# ATL Xpress: 30-minute established-session result and mobile login proposal

**PASS: isolated 100-established-session sustained qualification.** This is not production or million-user capacity qualification. No 500-session stage, merge, deployment, production release build, infrastructure or password-admission change occurred.

## Source, environment and unchanged gates

[GitHub Actions run 40549418096](https://github.com/3my1234/across-backend-api/actions/runs/40549418096) tested exact backend source `6e486729e3c8d285cf538350b5c657de419eefa1` on `perf/business-chat-history`. Workflow conclusion: `success`. Disposable-lab cleanup: `passed`.

API executable SHA-256: `16e17986f680b7c709e16f1a2065984057d4d3bd5e73c15cf6c9de0ca3158de7`. Identical to the earlier successful three-minute and mixed-workload optimized API: `True`. Lab executable SHA-256: `353ebdb0fe08beb16871abf24dacf507d31c46b45e60cb662796a2b0f30e99bb`. The application `internal/` diff against optimized baseline `a5e94496f90435a2610c3c46592af3d0ea33e055` is empty; the new source only adds lab queue observations. This is a longer qualification of the same API, not another performance optimization.

Existing standard GitHub runner, synthetic fixtures, isolated internal Docker network, no public ports or runtime external egress. No purchased resources. Limits: PostgreSQL 2 CPUs/1 GiB; two APIs each 0.75 CPU/512 MiB/pool 8; generator 1 CPU/512 MiB; Redis and gateway each 0.25 CPU/128 MiB. This is not an inventory or benchmark of production hardware.

Unchanged large-busy seed: 100 buyer/provider pairs, 1,000 products, 1,000 services, 100,000 marketplace messages, 205,000 shared-chat messages and 20,010 rooms. 100 distinct identities selected. Activities retain the existing 40% browse/search/details, 30% marketplace chat, 20% Town Hall, 5% Support and 5% admin mix; 3-12 second think times and independent 15-second conversation polling. There are no WebSocket connections in this workload.

Original gates remain per-operation p95 <500 ms and p99 <1,500 ms, unexpected error rate <0.5%, zero normal overload responses, no content/identity failures, and polling delivery p95 <17 s / p99 <20 s. No thresholds were relaxed. Queue completeness checks require continued observations through measurement and recovery; they do not substitute for worker delivery tests.

## Population, traffic and latency

All 100 identities performed real password logins gradually and had their tokens checked before measurement. Warmup is tagged separately and excluded from measured workload statistics. Simultaneous VU maximum: 100; sampled full-target dwell: 1799.999 seconds. The configured constant-VU measurement is 1,800 seconds; graceful completion can add a short drain window.

Measured HTTP window: `2026-10-27T17:22:49.048704+00:00` to `2026-10-27T17:52:52.596258+00:00` (1803.548 s). Offered 80,898, completed 80,898; status counts `{'200': 62300, '201': 12225, '204': 6373}`. Measured throughput 44.85 requests/sec, 23,968 completed journeys, 0 reported dropped iterations. This is a closed-loop, human-style session workload; there is no independently scheduled offered-arrival total or maximum-RPS claim.

Worst operation p95: **48.14 ms** (`services`). Worst operation p99: **58.37 ms** (`services`). Threshold failures: `[]`. Both APIs served traffic: `True`.

| Operation | Samples | p50 ms | p95 ms | p99 ms |
|---|---:|---:|---:|---:|
|admin_overview|1,179|2.02|2.64|4.43|
|catalogue|9,467|1.71|22.71|27.56|
|hall_history|4,792|2.66|3.84|5.31|
|hall_send|4,792|7.34|10.78|14.52|
|inbox|7,333|14.47|19.88|23.80|
|market_history|19,398|3.36|4.93|7.02|
|market_retry|100|3.92|7.28|16.99|
|market_send|7,333|5.69|8.43|12.57|
|product_detail|4,736|3.89|5.60|7.99|
|receipts|6,373|2.36|3.76|6.47|
|service_detail|4,731|3.37|4.69|7.01|
|services|9,467|0.91|48.14|58.37|
|session|563|3.87|5.16|6.39|
|support_history|634|1.78|2.43|3.63|

Application polling latency: `{'p(99)': 14685.16, 'max': 15012, 'min': 10, 'med': 5529, 'p(95)': 13787}` milliseconds. This includes the intentional polling interval; it is not phone rendering or push delivery latency.

## Correctness and reconciliation

**12,125 acknowledged logical message writes** reconciled against the offer/ack journal and database: `True`. Marketplace messages 7,333; Town Hall messages 4,792; offers 0; duplicate marketplace messages 0. Every acknowledged message key stored exactly once: `True`. Complete per-key inventory is in `reconciliation.jsonl`.

Every generated marketplace message was retrieved through its intended peer's actual paginated API: 7,333/7,333, across 200 pages. The normal workload also checks chronological marketplace pages, identity binding, cross-conversation exclusion, token validation and message-idempotency replays. Receipt-row inventory is 7,313; logical message persistence is the exactly-once claim, not proof of external notifications or every mutable metadata field.

## CPU, memory, pools and Redis

CPU percentages below use Docker accounting where 100% means one core, not a percentage of the whole host. Samples exclude warmup and recovery; short peaks are not a sustained-core capacity claim.

| Container | Samples | Mean CPU % of one core | Peak CPU % | Peak memory % of limit | Last memory |
|---|---:|---:|---:|---:|---|
|atlxpres-capacity-lab-sessions-run-451e4f822af0|228|7.01|15.79|25.03|127.7MiB / 512MiB|
|atlxpres-session-observer|228|0.03|0.20|11.23|10.28MiB / 256MiB|
|atlxpres-capacity-lab-gateway-1|228|1.00|2.17|9.18|10.82MiB / 128MiB|
|atlxpres-capacity-lab-api2-1|228|4.09|8.62|8.97|38.36MiB / 512MiB|
|atlxpres-capacity-lab-api1-1|228|4.21|8.82|9.18|39.68MiB / 512MiB|
|atlxpres-capacity-lab-redis-1|228|1.86|3.71|14.45|10.06MiB / 128MiB|
|atlxpres-capacity-lab-postgres-1|228|18.21|62.22|25.39|255.4MiB / 1GiB|

Pool counter deltas and maximum sampled occupancy:

```json
{
  "api1": {
    "samples": 359,
    "max_acquired": 1,
    "connection_limit": 8,
    "acquire_count_delta": 129361,
    "empty_acquires_delta": 1,
    "cancelled_acquires_delta": 0,
    "empty_acquire_wait_ms_total_delta": 4
  },
  "api2": {
    "samples": 359,
    "max_acquired": 1,
    "connection_limit": 8,
    "acquire_count_delta": 129641,
    "empty_acquires_delta": 1,
    "cancelled_acquires_delta": 0,
    "empty_acquire_wait_ms_total_delta": 5
  }
}
```

Redis measurements:

```json
{
  "used_memory": {
    "min": 1299160,
    "max": 2914712,
    "first": 1692408,
    "last": 1299160
  },
  "used_memory_rss": {
    "min": 13729792,
    "max": 15220736,
    "first": 13819904,
    "last": 14454784
  },
  "total_commands_processed": {
    "min": 3708,
    "max": 317702,
    "first": 3708,
    "last": 317702
  },
  "instantaneous_ops_per_sec": {
    "min": 4,
    "max": 364,
    "first": 100,
    "last": 4
  },
  "rejected_connections": {
    "min": 0,
    "max": 0,
    "first": 0,
    "last": 0
  },
  "evicted_keys": {
    "min": 0,
    "max": 0,
    "first": 0,
    "last": 0
  },
  "keyspace_hits": {
    "min": 1131,
    "max": 104249,
    "first": 1131,
    "last": 104249
  },
  "keyspace_misses": {
    "min": 52,
    "max": 21852,
    "first": 52,
    "last": 21852
  }
}
```

## Queue growth, age and recovery

359 measured queue/pool snapshots; observed measurement span 1795.35 s, maximum observer gap 5.08 s. Measurements continue 65.81 seconds after the final workload response. Observation completeness: `True`.

**Workers were intentionally off throughout.** Queue rows include fixture/lifecycle backlog; the net-growth column separates newly accumulated work from that baseline. Pending age shows how long work remains when no worker consumes it. This does not qualify worker CPU, delivery latency, vendor limits, retry handling or queue drain recovery.

| Queue | Before auth | Measured start | Measured end | Net growth | End of recovery | Oldest age at measured end (s) |
|---|---:|---:|---:|---:|---:|---:|
|email|8|58|3,716|3,658|3,717|1831.8|
|native_push|0|101|33,828|33,727|33,833|1799.0|
|web_push|0|0|0|0|0|0.0|

Email/web age uses delivery-row creation. Native-push age uses the parent notification creation timestamp because the delivery table has no creation column; it is an age proxy, not an exact enqueue timestamp. Status breakdowns, maximum counts and ages are preserved in `sustained-observations.json`.

Recovery readiness: 12/12 probes returned HTTP 200; range 2.248-5.210 ms. Recovery CPU observations are retained separately in `sustained-observations.json`. The disposable containers/network/volumes were then removed by the workflow cleanup step; raw evidence was uploaded before cleanup.

## Separate mobile login proposal

Local prototype commit `8d4f1626c74188a5fb6b729700b4f3d11751dba2` on `experiment/login-overload-proposal`, based on `3fd8df706d79e5b479266423cb6d155299cc2edb`. No entry point imports it and current app behavior is unchanged. Proposal, source patch and test output are retained with this campaign. Transport regression tests and a strict focused TypeScript check passed; existing session-startup tests passed. This is not a native build or physical-device qualification.

Proposed policy: only password login, only a completed HTTP 503 with valid Retry-After; server wait plus 0-1 s independent jitter; at most six total attempts or 30 seconds. Date-based headers require the same response's valid Date header, avoiding a shortened wait due to phone-clock skew. Do not automatically retry registration, 429, other error statuses or ambiguous network/timeouts. Show a friendly busy/wait notice and a working Cancel control.

Before enabling the prototype, separately review and implement a synchronous submission lock, disabled competing submissions/credential edits, lifecycle cancellation, generation checks, and serialized SecureStore/login/OAuth/logout session commits. The transport tests do not prove those UI/storage integration behaviors. Then exercise the actual integrated policy against isolated login bursts and physical devices. Password hashing and backend admission/rate limits stay unchanged.

See `MOBILE_LOGIN_OVERLOAD_PROPOSAL_2026-10-27.md` for the complete proposed behavior and acceptance checklist.

## Review boundary

This result qualifies only the stated established-session workload and dataset in this lab. Driver background GPS, concurrent media uploads/downloads, external push/email delivery, worker drain, real billing, production network/hardware, longer soaks and 500+ sessions remain unqualified. The previously documented generic-plan/deep-history limitations are not resolved by this stage. A large registered-user count cannot be inferred from this result.

No further phase was started. Review this evidence and the login proposal before choosing the next isolated test or integration work. In particular, worker delivery/drain needs separate qualification rather than assuming that the HTTP session result covers it.
