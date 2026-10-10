# Atlantic Express production scaling on Coolify

The main Dockerfile builds these processes. A separate `Dockerfile.worker`
starts the worker by default for Coolify Dockerfile resources:

- API: `/app/across-api`
- durable background worker: `/app/across-worker`
- schema migration job: `/app/across-migrate`

Email and push delivery use PostgreSQL outbox tables with row leases,
`FOR UPDATE SKIP LOCKED`, retries, and idempotency. This is the durable queue.
Redis is used for cross-replica rate limiting and short-lived public catalogue
caching. Losing Redis therefore does not lose an order, payment, email, or push
job.

## Safe migration from the current single Coolify resource

1. Deploy the new backend normally with `RUN_INLINE_WORKERS=true`. This is
   backward-compatible and applies all migrations.
2. Add a private Redis resource in Coolify with persistence and a password.
   Set `REDIS_URL` and `REDIS_OPTIONAL=false` on the API.
3. Duplicate the backend resource as **atlxpres-worker**. Use the same repository,
   branch, database, Redis, SES, Expo, and application secrets.
   In the worker resource's General/Build configuration, set **Dockerfile Location**
   to `/Dockerfile.worker` (keep the existing Base Directory). The main API resource
   must continue using `/Dockerfile`.
   Leave `DATABASE_READ_URL` blank on worker resources because workers never
   serve catalogue reads. Start with `DB_MAX_CONNS=10` and `DB_MIN_CONNS=1`.
   Do not attach a public domain to the worker.
   Configure its internal health check as `/api/v1/health` on port 8080.
   The worker also accepts `/` for Coolify's default HTTP probe. Both report
   `service: worker` and consumer heartbeat health; unknown paths return 404.
   Keep `RUN_INLINE_WORKERS=false` on the dedicated worker; its worker entry point
   runs background jobs independently of that API-only setting.
4. Redeploy the worker from the same release as the API. Confirm the worker logs
   show `Atlantic Express background worker started`, and its terminal command
   `ps -o pid,comm` shows `across-worker` rather than `across-api`. A healthy API
   running under a resource named worker does not process queues with inline
   workers disabled. Inspect the queued email's attempts/status after startup.
5. Change the API resource to `RUN_INLINE_WORKERS=false` and redeploy it.
   This prevents the API replicas from also running the worker loops.
6. Scale the API to at least two replicas behind Coolify's proxy. Scale workers
   independently when email/push queue age grows. Queue claiming is replica-safe.

## Dedicated migrations before PgBouncer

PgBouncer transaction pooling is incompatible with session-level advisory locks.
Before pointing API replicas at a transaction-pooled URL:

1. Create a non-public **atlxpres-migrate** resource from the same image.
2. Set its command to `/app/across-migrate`.
3. Set `MIGRATION_DATABASE_URL` to the direct primary PostgreSQL URL,
   never the PgBouncer transaction-pool URL.
4. Run this one-shot resource once per backend release before rolling out API
   replicas.
5. Set `RUN_MIGRATIONS=false` on every API replica.

Until the migration job is configured, leave `RUN_MIGRATIONS=true` on the
single current API so deployments remain backward-compatible.

## PostgreSQL and PgBouncer

Use a managed PostgreSQL service with automated backups, point-in-time recovery,
multi-zone failover, monitoring, and PgBouncer (transaction mode). Put the
PgBouncer URL in `DATABASE_URL`. Configure `DB_MAX_CONNS` so:

```
(API replicas x DB_MAX_CONNS) + (worker replicas x DB_MAX_CONNS)
  < PgBouncer/server connection limit
```

A safe initial value is 20 connections per API replica and 10 per worker.
The previous hard-coded 80 connections per process would exhaust PostgreSQL as
replicas were added; connection budgets are now configurable.

When a managed read replica is available, set `DATABASE_READ_URL`. Public
catalogue product reads use it; payments, checkout, inventory, accounts, and all
writes continue to use the primary database. Replication lag is therefore never
used as proof of payment.

## Health checks and scaling signals

- Liveness: `/api/v1/health`
- Readiness: `/api/v1/ready`

The dedicated worker detects polling loops that stop completing. Email allows
ten minutes, push/receipt loops two minutes, settlement loops 45 minutes,
batch closure five minutes,
and hourly auto-confirm two hours. A watchdog exits a stalled process so the
container restart policy can recover it. Confirm an automatic restart policy is
enabled on the deployed container; do not rely on an unhealthy Docker status alone
to restart a process. Queued jobs remain in PostgreSQL and expired leases are
retried. SMTP/provider failures are retried as jobs, rather than forcing a process
restart for each failed delivery. The worker binds its health port before starting
consumers and fails startup if that port cannot be bound. Readiness dependency
checks have a three-second deadline.

Loop heartbeats establish that polling is running, not that every external
delivery succeeds. Monitor `/api/v1/admin/ops/queue-health` and delivery failure
logs for growing backlogs, dead letters or SMTP outages; additional worker
replicas and operational alerts still require deployment configuration.

Readiness verifies primary PostgreSQL, the optional read database, and required
Redis. It also reports current database pool usage. Email, storage, and Google
authentication configuration are reported as capabilities but do not remove all
API replicas from service because one external provider is temporarily slow.
Authenticated admins can inspect `/api/v1/admin/ops/queue-health` for
pending email/push counts, oldest-job age, and database-pool usage.

Scale API replicas based on sustained CPU, memory, p95 latency, request rate,
database-pool saturation, and HTTP 5xx rates. Scale workers based on the oldest
pending outbox job and pending-job count. A larger single server is only a
temporary capacity increase, not the million-user architecture.

## CDN and mobile behavior

Cloudflare/CloudFront remain the first cache layer for public assets and
catalogue responses. Redis supplies a five-second shared origin cache so every
API replica sees the same short-lived entry. Mobile foreground polling is only
a fallback; push notifications are the immediate update mechanism.

No EAS build is needed for API/worker scaling alone. The currently unbuilt
mobile changes from commit `74fdb22` still require one new EAS build after
the backend and provider portal are deployed and smoke-tested.
