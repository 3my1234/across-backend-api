# Spike readiness — prepared, not activated

## Verified inventory, 16 October 2026

The operator's main-host output shows 4 CPUs, 7.8 GiB RAM, one API, one worker,
Redis, Traefik 3.6, Coolify 4.4.6 and the web applications. Compose is v5.3.0;
the shared Docker network is `coolify`. PostgreSQL runs on the separate server
over WireGuard. This is not a highly available application or database setup.
Idle memory and a healthy readiness endpoint do not establish traffic capacity.

The provider container in that output is still on `6ed90e63`, whereas the admin
container is on `d99a15b2`. Deploy the provider resource separately for its latest
support-cache changes; an admin deployment does not update the provider PWA.

## API safeguards in this branch

- `HTTP_MAX_IN_FLIGHT` defaults to 128 per API process; overload returns 503 with
  `Retry-After: 2` before business handlers, even if Redis is unavailable.
- Liveness and readiness bypass admission. Readiness has a shared two-second
  dependency-check deadline. It must be the load-balancer probe; `/` is insufficient.
- SIGTERM makes readiness fail, waits three seconds for routing removal, then
  drains requests for up to 65 seconds. Set container stop grace to at least 75s.
- HTTP read/write timeouts are 60s and idle connections 30s. They do not cancel
  every handler or imply an external payment was rolled back. Do not automatically
  retry writes after ambiguous network failures.
- `api_capacity` logs every 30s contain active/rejected requests and pool wait
  counters, without tokens or message bodies. Counters reset on process restart.

These limits shed excess work; they are not a measured capacity certification.
Existing shared Redis limits/cache and durable worker claims are retained.
Admission includes webhooks: a rejected webhook must be retried by its sender.
Monitor webhook delivery and reconciliation during overload.

## Isolated two-replica pilot on the existing main host

`deploy/scaling/compose.yml` and `haproxy.cfg` are an opt-in pilot, **not a cloud
autoscaler or production cutover**. They intentionally contain no production
domain labels. The gateway binds only `127.0.0.1:18080`. No new database is created.
The existing API remains the production route and rollback path throughout.

Prerequisites: operator-approved immutable API image; private environment file
outside the checkout, mode 600, containing the existing production configuration;
verified migrations already applied once; confirmed DB connection budget. Do not
paste the private file into chat, commit it, or print resolved Compose config.
Check that image exists locally with `docker image inspect` before starting.

Do not run migrations or inline workers in API replicas. Their env overrides in
the pilot set both false and require shared Redis. The worker remains independent.
All instances must share signing keys, auth configuration, PostgreSQL, Redis and
S3; no session affinity or local upload directory is assumed.

Run the connection gate first, using **actual** PostgreSQL max_connections and
the maximum connections of all existing API/worker/admin/migration clients.
Count old and new containers during rollout and a reserve for operator recovery:

```sh
# Example inputs only: 100 DB slots, 20 reserve, 40 other clients,
# two pilot replicas, one surge replica, pool 8, no second pool on the same DB.
node scripts/scale-budget.mjs 100 20 40 2 1 8 0
```

The gate must pass before increasing replicas. This arithmetic protects connection
slots; it does not prove the database has spare CPU, I/O or query capacity.
If DATABASE_READ_URL creates another pool against the same PostgreSQL server,
include its full per-instance size in READ_POOL_ON_SAME_DB. Avoid configuring a
second pool accidentally through a cosmetically different URL to the same DB.

After exporting `ATL_API_IMAGE`, `ATL_RUNTIME_ENV` (absolute private file path)
and optionally `ATL_SHARED_NETWORK=coolify` in the operator terminal:

```sh
docker compose -f deploy/scaling/compose.yml config --quiet
docker compose -f deploy/scaling/compose.yml up -d --scale api=2
curl -fsS --max-time 5 http://127.0.0.1:18080/api/v1/ready
docker compose -f deploy/scaling/compose.yml ps
```

All writes through this pilot still reach production data. **Do not run load tests
against it.** Use synthetic staging accounts against an isolated staging DB/Redis
for capacity testing. A dedicated staging stack can use the same definitions but
must supply its own runtime file. Do not use customer tokens as test fixtures.

Test failover in staging: stop one API while issuing reads through the gateway,
verify requests succeed after the four-second health-removal window, restart it,
verify recovery. The gateway never retries writes across replicas. Test changing
the number of replicas, DNS refresh, authentication across instances, shared rate
limits, duplicate webhook processing and message receipts. Compare per-container
logs and gateway backend statistics to verify both replicas serve traffic.

Rollback/remove the pilot with `docker compose -f deploy/scaling/compose.yml down`.
Never add `--volumes`, stop the existing API, alter DNS or change Traefik routes as
part of this pilot. Production routing requires a separately reviewed cutover.

## Proposed cloud expansion (requires cost approval)

Prefer one region for API, worker, PostgreSQL and Redis. Do not attach cloud API
tasks to the current database over the public internet or assume WireGuard on a
Contabo host automatically extends into AWS. Benchmark latency from Nigeria before
selecting the region; existing S3 assets are in eu-north-1.

Target design: protected CDN/WAF → managed load balancer → ECS Fargate API service
across two availability zones; separate worker service; managed PostgreSQL with
standby and tested restore, bounded connection pooling; shared managed Redis;
S3 for media. Provision privately with explicit network/security rules. Keep
private chats, support and authenticated responses out of shared CDN caching.

Start with two API tasks, 1 vCPU/2 GiB each. Initial autoscaling maximum is a
**budget and database decision**, not a target of millions: propose 10 tasks,
CPU target 55%, memory alert 70%, 60s scale-out and 300s scale-in stabilization.
Use measured request concurrency/latency to refine CPU-based scaling. Add worker
capacity based on queue age, not merely API CPU. Reserve deployment surge and
service quotas. DB saturation must trigger load shedding rather than endlessly
adding API tasks. Cap total backend connections before lifting the task maximum.

No autoscaling platform instantly absorbs a million-client surge from two tasks.
Keep warm capacity based on measured demand, validate cloud quotas in advance,
and provide a busy response/waiting room for excess load. Budget alerts are not
an instantaneous billing cap. Cloud spend includes network, logs, database, cache,
media, messages/email and migration overlap as well as API compute.

Before cloud activation: approved itemized estimate and resource ceiling; private
network and secret-store plan; staging load/failover/restore results; incremental
cutover with rollback; independent uptime/error/latency/queue/DB alerts. Remove
frequent catalogue/chat polling through push-driven invalidation and bounded
delta sync before claiming mass-concurrency readiness. One million clients polling
once per 15s alone means about 66,667 requests/s, excluding writes and browsing.

## Capacity acceptance

Run `scripts/chat-capacity.mjs` only in isolated staging; it refuses production
origins. Its current 100-VU test is an initial check, not a million-user benchmark.
Increase synthetic users and workload gradually, including Town Hall reads/writes,
subscriptions, image delivery and support, with production-like query cardinality.
Track p95/p99 latency, error and rejection rates, DB waits/CPU/I/O/locks, Redis,
network, queue age and cost. Stop on overload or breached limits. Record the tested
request rate, connected users, workload, dataset, machine sizes and test duration.
Only use those results to set an advertised or operational capacity.

References checked 16 October 2026:
- https://coolify.io/docs/core/infrastructure/scaling/multi-server-deployments
- https://coolify.io/docs/core/infrastructure/scaling/cloud-load-balancing
- https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service-auto-scaling.html
- https://aws.amazon.com/fargate/pricing/
- https://aws.amazon.com/elasticloadbalancing/pricing/
- https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-best-practices.html
