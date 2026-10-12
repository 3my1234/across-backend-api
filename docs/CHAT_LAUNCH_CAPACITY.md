# Chat launch capacity and deployment

## What this release changes

Conversation lists default to 20, message history to 50, with cursors for older
items. Private client memory holds up to 10 recent threads, expires after five
minutes and clears on account changes. It is not browser offline storage.
Provider chat refreshes use an authorized revision check: unchanged history is
HTTP 304, avoiding another message-page query and image signing. A committed
message changes the revision even when timestamps/commit order differ. Private
image URLs refresh periodically. CORS allows If-None-Match and exposes ETag.
Chat reads have a five-second database deadline. Unread counts use a composite
index; paged history and inbox indexes already existed. Message submission and
its provider activity/push event commit together; delivery happens in workers.

PostgreSQL remains the source of truth. No public Redis cache or service worker
stores private chats. Pooling lets many HTTP users share a bounded number of DB
connections; one user does not require one persistent PostgreSQL connection.
Current clients still poll while foregrounded; background notifications are not
a replacement for a live chat transport. WebSockets/SSE with authenticated event
routing and cross-replica pub/sub are a later measured optimization, not a feature
being claimed in this release.

## Deployment order

1. Apply migrations 072–074 on the direct primary before deploying this API.
   With the current single API and no PgBouncer, RUN_MIGRATIONS=true applies them
   on redeploy. Or run /app/across-migrate once in the API container using the
   direct primary URL. The variable is plural RUN_MIGRATIONS.
2. Redeploy API and worker from the same release. Keep the worker Dockerfile
   /Dockerfile.worker and RUN_INLINE_WORKERS=false on both dedicated resources.
3. Deploy the portal. Publish a new mobile build once native photo changes and
   conditional polling are ready; server/index changes alone need no APK.
4. Confirm /api/v1/ready and worker health; review admin ops queue-health.

## Connection budget, not an arbitrary increase in connections

Inspect max_connections using scripts/diagnose-capacity.sql. Reserve connections
for migrations/maintenance and calculate ALL pools. Example, if PostgreSQL has
100 available slots: 2 API replicas x 20 + 1 worker x 10 = 50, leaving 50 for
other resources and headroom. This is an example, not measured server sizing.
Use DB_MIN_CONNS=1. Include configured read pools in their own server budgets.
Do not add API replicas while ignoring this sum. An API replica cannot increase
the capacity of an already saturated DB or a single physical host.

If adopting PgBouncer transaction pooling, follow COOLIFY_SCALING.md: migrations
must connect directly because their lock is session-scoped. The API's pgx
configuration also needs a tested transaction-pool-compatible query mode;
do not switch production URLs without testing the driver and PgBouncer
prepared-statement configuration. Keep chat/order reads on the primary for
immediate consistency. See PRODUCTION_SCALING_ROADMAP.md for the actual 4-vCPU/8-GB server
budget, operational thresholds and staged infrastructure split.

## Repeatable checks before launch

Local opt-in tests use isolated randomly named schemas on loopback PostgreSQL.
They never load-test production or modify its public tables:

    SETTLEMENT_TEST_LOCAL=true go test ./internal/controllers -run 'TestChatConditional|TestChatUnreadIndex' -count=1 -v

Observed locally: 100 concurrent in-process history checks, four pooled
connections, p95 about 84 ms. A 50,000-message unread-count fixture used the
range index. These do not measure the deployed server, mobile network, peak
write traffic, or long-term growth. Hardware timing is deliberately not a
unit-test pass/fail threshold.

Use scripts/chat-capacity.mjs with k6 on a staging copy containing synthetic
users/history. It ramps 20/50/100 foreground chats, reads only, limits payloads,
uses conditional requests and checks p95 <500ms/error rate <1%. Prepare at least
100 synthetic credentials in a private JSON file outside Git, each containing
token and conversation_id belonging to that user. Do not print tokens or use
production customer fixtures.

    CAPACITY_TEST_STAGING=yes STAGING_ORIGIN=https://staging-api.example.com CHAT_FIXTURES_FILE=/private/chat-fixtures.json k6 run scripts/chat-capacity.mjs

Increase the next staging load only after the previous level passes. Run a
separate staging write/reconnect test before promising a larger user capacity.
The script intentionally refuses the production API hostname.

The default API IP rate limit is 600 requests/minute. A load generator running
all 100 users behind one IP can hit that limit. For the isolated staging test,
use realistic distributed sources or an appropriate staging-only limit (e.g.
RATE_LIMIT_PER_MINUTE=2000). Do not weaken production abuse controls to make a
benchmark pass; include NAT/mobile-carrier traffic in subsequent rate-limit
validation.

## Monitoring and growth

Admin /api/v1/admin/ops/queue-health exposes acquired/idle/max DB connections,
acquire wait/count, exhausted-pool waits and cancellations, plus pending email,
Expo and browser-push queue age/count. Counters are cumulative per API process:
compare deltas over time, not absolute totals. Export to your monitoring system
and alert on sustained pool waits, p95 >500ms, elevated 5xx, lock waits, growing
notification age, disk space, backups and restore failures. Run the read-only
diagnostic SQL for PostgreSQL activity/index/vacuum signals.

Set up external alerting and tested backups/PITR in your hosting environment.
Those are operational setup steps; this repository cannot activate them in
Coolify. A readiness endpoint proves dependencies respond, not that a million
users can be served. Larger deployments use independently scaled API/workers,
live message routing, partitioned storage when justified, and measured capacity.
