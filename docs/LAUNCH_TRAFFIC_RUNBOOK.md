# Launch traffic protection and capacity gate

Scope: current Contabo/Coolify servers. Do not execute the cancelled AWS
foundation/application deployment. This release does not enable autoscaling.

## What is protected

- The API admits at most `HTTP_MAX_IN_FLIGHT` application requests (default 128).
  Excess requests receive 503 and Retry-After before handlers or writes execute.
  This bounds application work; it does not protect against network saturation.
- Shared Redis rate limits and public catalogue caching complement admission.
  Private conversations are never put in the public cache.
- Catalogue version hints share one primary-DB read per second per API process.
  Actual catalogue reads still revalidate the primary revision.
- Updated mobile/PWA catalogue watchers poll every 15–18 seconds, pause in the
  background, back off on failures and honor Retry-After. They do not trigger a
  full catalogue reload when the heartbeat fails. Opening/foregrounding and
  manual refresh remain immediate. Chat refresh intervals are unchanged.
- Static account/overview page polling is reduced to 60 seconds. Support and
  conversations retain their faster refresh and local history behavior.
- Migration 092 adds creation-order public Town Hall and buyer-history indexes.
- The Production readiness workflow checks the public readiness endpoint on a
  five-minute GitHub Actions schedule with three attempts. Schedules can be
  delayed. Enable Actions failure notifications; this is not a paging SLA.

## Rollout

1. Deploy backend main in Coolify. Keep RUN_MIGRATIONS=true on the API, false
   on the worker. Verify migration 092 and `/api/v1/ready` returns ok=true.
2. Confirm RUN_INLINE_WORKERS=false on the API because the dedicated worker is
   deployed separately. Do not assume an unset value means false.
3. Deploy the worker from the same tested main revision; verify its readiness.
   Do not duplicate worker loops by scaling the API with inline workers enabled.
4. Deploy BOTH admin and provider resources from admin-web main. These are
   separate Coolify deployments. The provider service worker is shell v19.
5. Build mobile from the updated mobile main. Older APKs retain older polling.
6. Verify first and cached chat open, support refresh, sending and receiving,
   Town Hall feed/history, manual catalogue refresh, foreground refresh, and PWA
   background/resume. Do not replay a failed write automatically.

Use dependency readiness (`/api/v1/ready`) for traffic admission health checks;
the shallow root or health endpoint alone does not prove dependencies work.
Check the configured startup grace period before changing a live health check.

## Evidence and its limits

`SETTLEMENT_TEST_LOCAL=true go test ./internal/controllers -run
TestCommunityLaunchMixedLoad -v -count=1` uses isolated loopback PostgreSQL,
2,000 Town Hall requests, 20,000 messages, an eight-connection pool and the
default admission limit of 128. It exercises mixed reads/sends, bounded pages,
the feed index, private history access and rejected-write side effects.

On the development machine, a 100-request burst completed without rejection;
a 400-request burst exercised controlled overload. Successful-request p95 was
about 184 ms and 172 ms respectively in one run. The initial 20-request cold
burst reached about 1.15 seconds p95: cold latency still needs attention.
These are in-process controller tests, not concurrent-user capacity claims.
They omit network/authentication, populated offers, media and production hardware.

Before advertising a supported traffic number, run `scripts/chat-capacity.mjs`
against an isolated staging deployment with synthetic accounts and a database
similar in size to launch expectations. The script rejects production origins.
Its current 20/50/100-user chat scenario is a starting point, not a million-user
qualification and not a full Town Hall write-load test. Keep synthetic tokens
private. Record successful-request p95, errors, requests/sec, pool waits, CPU,
Redis and worker queue age together. Stop increasing load if correctness fails.

## Observe and expand

At launch, retain a baseline and compare repeated snapshots:

- Main and DB hosts: `docker stats --no-stream`.
- API logs: `api_capacity`; rejected is cumulative, so compare deltas.
  DB wait time and cancelled acquisitions are also cumulative.
- Database: run `scripts/diagnose-capacity.sql` in psql; it is read-only and
  does not display private messages or credentials.
- Worker: check readiness, delivery failures and growing outbox/queue age.

Treat sustained CPU above 70%, successful-request p95 above 500 ms, rising
database acquisition waits, repeated admission rejections or growing queue age
as investigation/expansion triggers. These are initial operating thresholds,
not measured capacity guarantees. Investigate SQL/DB waits before adding API
copies; replicas cannot fix a saturated shared database.

As verified on16 October2026, the two API copies under `deploy/scaling` are
connected to production HTTPS through HAProxy/Traefik, with the original API
retained as fallback. Operator readiness distribution, stop-one/recovery and
customer/provider device checks passed. The current connection budget is84/100
including reserves and one surge copy. This is operational evidence, not a
throughput certification. See [managed replica releases](../deploy/scaling/RELEASES.md)
for the separate installation needed to automate updates safely. Until installed
and verified, roll back replica routing before changing the original API release.
More copies on the same four-core host do not add physical compute or survive
that host failing. Additional hosts or managed compute remain a separate
deployment and spending decision. There is no automatic capacity creation today.
