# Hot-room queue review

Current application candidate: `baa03e4aea43db055c2a0e7622e20dc047a1f23a`.

The reviewers correctly distinguished disposable performance from VPS qualification. They also identified a real gap in the initial scheduling correction: its queue respected a cancellable context but had no independent deadline. That gap is corrected in this candidate.

## Answers to the five implementation questions

1. Waiting occurs in memory before acquiring a database connection. Queue residence is limited to **two seconds**, independently of fasthttp's disconnect semantics. At most **32 waiters plus one active writer per room per API instance** are admitted. A full queue or expired queue deadline returns **503**, `Retry-After: 2`, `Cache-Control: no-store`, before any transaction starts. The client should retain the same message reference when retrying. This is an overload bound, not a change to the 500 ms performance gate or an end-to-end transaction timeout.
2. Keys are canonical room UUIDs. Different rooms do not share their waiting token. The short map mutex only manages queue references; it is never held while waiting or accessing PostgreSQL. Empty entries are removed, cancellations drop references, and releases are idempotent.
3. Each API replica has its own room scheduler. PostgreSQL still arbitrates between replicas and all closure/membership writers. At most one locally scheduled writer per replica takes a connection for a given room. A real database regression uses two independent controllers, an external row-lock holder and 16 concurrent HTTP retries: unrelated queries retain capacity and the retries persist one message and one notification per recipient. Paid access, membership and closure checks remain after the authoritative row lock.
4. The disposable authenticated replay uses the original qualified k6 source, original mixed workload, unchanged hot-room selection, normal independent rooms, polling and think times. It is not the separate deliberately synchronized Windows diagnostic. No traffic distribution was changed to get a pass.
5. The test API group shares **one CPU / 2.5 GiB**, PostgreSQL has **two CPUs / 3 GiB**, total **three CPUs / 5.5 GiB**. There are **two API pools of eight connections**. Test HTTP admission is **64 requests per replica** (the application default is 128); neither was increased. The generator remains capped at two CPUs / 4 GiB. Hardware still differs from Contabo.

## Verification

- PostgreSQL 16 release regression and race checks: [42245174772](https://github.com/3my1234/across-backend-api/actions/runs/42245174772), passed. Required tests explicitly executed: cancellation/UUID aliases/independent rooms, queue backlog bound, actual HTTP timeout before database access, and cross-controller pool/retry atomicity.
- Original unbounded-candidate authenticated replay: [42223698018](https://github.com/3my1234/across-backend-api/actions/runs/42223698018), 100 sessions / 180 seconds, p95 7.21 ms, zero unexpected errors, 1,513 offered/acknowledged/stored writes, full cleanup. This is historical evidence for source c2da06d, not a result for the newly bounded source.
- Exact bounded candidate replay: [42245254946](https://github.com/3my1234/across-backend-api/actions/runs/42245254946), **passed**. No live VPS test or production change authorized by this review.

The original VPS20 pass / VPS100 latency failure remains the latest actual-server capacity evidence. The former single unconfirmed committed write remains classified as consistent with an in-flight-at-abort response, not proven delivered or erased from history. Retry regressions verify exactly-once persistence semantics; they do not replay the deleted historical database row.

The reviewed live sequence remains20 ->100 ->gradual500 ->500 for30minutes, with original stage durations and acceptance gates. Adding250 would be a change to that sequence. Reconcile each stage; tear down the isolated environment after completion or abort. Approval of a reviewer recommendation is not user authorization to run a live test.

## Exact bounded-build results

100 authenticated sessions for the full180seconds.9,361measured requests, zero unexpected errors, zero controlled overload responses. All original overall/per-operation latency, content, session and write gates passed.

| Operation | p95 milliseconds |
| --- | ---: |
| Overall | **8.33** |
| Private message send |9.85|
| Town Hall offer |19.09|
| Town Hall send |9.91|
| Inbox |9.92|
| Catalogue |47.39|
| Service browsing |27.55|

Overall p9941.14ms, maximum successful latency117.89ms. **1,513offered =1,513acknowledged =1,513stored**, no missing/duplicate/unknown/unconfirmed writes. Normal completion and independent reconciliation passed; cleanup completed. Both running API executable hashes and the downloaded binary match `5c42473a27a48d3c003d4047d438c31005907fbca2cf85f7a7e1e51f54a7fb16` (38,904,071bytes).

These are disposable-run timings, not a direct comparison to VPS753ms or a500-session qualification. The mixed replay did not collect per-request pool-wait/lock-wait percentiles. Earlier local matched traces measured those independently; the replay's sparse database samples cannot prove there were no brief waits. Mixed worker drain and external device/email delivery remain outside this result.

Evidence: `qualification/messaging-100-42245254946/`, `RESULT_BOUNDED_MESSAGING_100_42245254946.json`, checksum manifest `messaging-100-42245254946-sha256.json`, and `room-write-bounds-ci-42245174772.log`. The old7.21ms result belongs to the earlier c2da06d binary; current bounded source is baa03e4 and current p95 is8.33ms. No further application optimization was made beyond closing the queue availability gap.
