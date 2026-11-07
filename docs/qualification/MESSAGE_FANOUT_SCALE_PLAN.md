# Message processing for 100, 1,000 and 10,000 sessions

## Capacity statement

This candidate changes how message notification work is scheduled. It is not a
capacity certification at any of those concurrency levels. No production change
or further VPS trial is authorized by this document. The last final VPS attempt
is closed. Commands/operators must be run by the user, not launched automatically.

The latest VPS result passed 20 sessions, then aborted 100 at overall p95 720ms.
The two API pools accumulated approximately 59 seconds of combined acquisition
waiting in the initial five-second burst. Notification fan-out was the largest
recorded SQL workload. These observations justify shortening that transaction;
they do not prove that every slow request is caused by notification processing.

## Implemented candidate

- `110_durable_message_notifications.sql` adds an indexed durable notification
  job table. Provider conversation events preserve their existing inline path
  unless that message transaction explicitly opts into asynchronous fan-out.
- `community_controller.go` snapshots eligible active room participants and
  blocking exclusions in the authorized message transaction. It commits one job
  instead of running all notification/delivery triggers while holding the room.
- `provider_conversation_controller.go` opts buyer provider-activity transactions
  into the same durable queue. Provider-to-buyer notification work moves from
  the previous best-effort post-commit call into the message transaction's job.
- `message_fanout.go` in services processes 64 recipients per transaction.
  `FOR UPDATE SKIP LOCKED` supports concurrent workers. Notifications, existing
  native/web delivery triggers and the recipient cursor commit together.
  Per-recipient event keys retain idempotency. Failed pages roll back and retry
  with bounded backoff; they are never silently dropped. Page scheduling yields
  to other jobs so a large room does not monopolize the queue.
- The `message_fanout` worker role runs one database transaction at a time, with
  four pages per polling cycle and a one-second ticker. Completed jobs older
  than seven days are pruned in bounded batches. Pending jobs are never pruned.
  This uses the configured worker pool; it does not silently increase its size.
- `ASYNC_MESSAGE_NOTIFICATIONS=false` is the default. Existing room locks,
  closure/membership checks, paid eligibility, message references, cache TTLs,
  API responses and qualification thresholds are unchanged.

With the flag enabled, notification rows become visible after the worker drains
their job. Message/history visibility remains immediate after message commit.
Recipient selection and event timestamps are captured at commit. Existing
delivery triggers resolve sender display information when the worker creates
delivery rows. Deleted recipient accounts are skipped rather than recreated.
No real phone, SMTP or external push delivery has been certified here.

Provider activity web-push work and buyer email queueing are not fully moved by
this candidate. Their contribution to remaining private-message latency must be
included in the next offline mixed comparison. Moving work off the HTTP path
does not remove its total database CPU cost.

## What each workload requires

| Active sessions | One five-second poll per session | Required evidence |
| --- | ---: | --- |
| 100 | 20 requests/second | Full mixed stage passes unchanged latency/error gates; notification jobs and delivery inventory drain correctly |
| 1,000 | 200 requests/second | API/worker throughput and database connection budget stay bounded; queue age does not grow throughout the stage |
| 10,000 | 2,000 requests/second | Sustained measured request/write mix, bounded queue age, pool/CPU/I/O headroom and complete correctness reconciliation |

Those rates are per polling stream. Three independent streams would generate
60/600/6,000 requests per second respectively, before sends or browsing. No polling
cadence, workload mix, concurrency count or threshold has been changed to pass.
Existing conditional polling/caching must be measured; this candidate does not
introduce WebSockets or claim that caching eliminates authenticated database work.

The connection budget is fleet-wide: API replicas times their pool limit, plus
worker replicas times their pool limits, plus an operational reserve. API or worker
replicas must not be added blindly: PostgreSQL must sustain the resulting work.
No additional servers, read replicas, connection proxy or orchestrator is purchased
or installed here. The existing two VPSs are not declared sufficient for 10,000.

This also does not remove business serialization inside one Town Hall room.
10,000 marketplace sessions spread over rooms is a different workload from
10,000 simultaneous writes to one room. Existing provider limits and retryable
overload protections remain; controlled overload cannot silently count as a pass.

## Validation and rollout gates

Tests cover rollback, recipient/blocking snapshots, deleted accounts, exactly-once
HTTP retries, failed-page atomic rollback/recovery, concurrent workers, fair pages,
retention and recipient inventories of 100, 1,000 and 10,000. Those inventory sizes
are not concurrent HTTP user tests. Native delivery row counts and a matched
ten-recipient native/web trigger profile retain the real notification work.

The initial PG18 Windows local profile measured the room-lock-to-commit median
at 5.79ms inline versus 1.11ms queued, 24 measured samples per mode in ABBA order.
Workers drained outside the timed section. These are diagnostic timings, not
VPS predictions or overall request p95 improvements. Full PG16 regressions,
race checks and the same profile are mandatory in CI.

Before enabling the flag in any release: review/apply migration110, start the
selected `message_fanout` consumer, budget its connections/CPU, and monitor pending
job count, oldest age, retries and cursor progress. Durable storage alone is not
backpressure: sustained arrival above drain throughput must trigger admission
control or increased verified worker capacity before disk/latency limits breach.
Backlog admission/alert wiring is not implemented by this candidate; the flag
must stay disabled until those controls and the mixed replay are verified.

Then compare baseline/candidate mixed traffic offline at 100 sessions under the
same caps. Require unchanged client/per-feature latency gates, zero unexpected
errors/overload, full message inventory and eventual notification/delivery counts,
with bounded delivery lag. Only measured improvement can justify another user-
authorized VPS test. 1,000 and 10,000 remain qualification targets, not promises.

Rollback: disable the API flag first and retain the new consumer until every
already committed job is drained. Old producers still work with migration110's
default inline trigger path. Keep the additive schema; never drop pending jobs
or stop their only consumer while rolling back application code.
