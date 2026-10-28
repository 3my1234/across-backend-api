# K3s preparation: not an installed or qualified cluster

Prioritize application/receipt and expanded-workload tests. These files prepare a
reviewable Kubernetes configuration while those tests run. They do not install
K3s, create VPS nodes, change Coolify/DNS/firewalls, provide credentials, publish
images or authorize any production action. No cluster smoke or failover test has
been performed. YAML parsing is not Kubernetes runtime acceptance.

`staging.yaml` uses a reserved `.invalid` hostname/image registry, zero digest
placeholders and TEST-NET private-endpoint placeholders. Supply actual isolated
staging values and exact reviewed image digests before any deployment review.
Never substitute production credentials to make staging start.

## Boundaries and source checks

- API image/command correspond to the existing Dockerfile and `/app/across-api`.
  The liveness route `/api/v1/health` returns a process response; readiness uses
  `/api/v1/ready` and checks dependencies. Database outages must eject replicas
  from routing without a dependency-driven API restart storm.
- The actual API drains for three seconds and allows up to 65 seconds for
  shutdown. A 90-second pod termination grace includes that window. Do not use a
  30-second grace and assume existing requests can finish. Verify connection
  draining and ingress timeout behavior against actual traffic in staging.
- Worker health endpoints are served by the existing separate worker image on
  `WORKER_HEALTH_ADDR=:8080`. Only email/native-push/receipt roles are selected;
  financial and other consumers are not duplicated by an HPA. Inventory and
  qualify omitted roles before describing the whole platform as migrated.
- Worker replicas deliberately start at zero. The normal worker hard-codes the
  public Expo endpoint. The capacity lab mocks it using a lab-only transport
  adapter; that is not yet a tested mock route in the real worker image on K3s.
  Keep Internet egress denied until an isolated adapter/proxy path and transport
  tests are approved. Do not send real notifications to qualify staging.
- No migrations run on API/worker startup. `migration-job.example.yaml` is a
  separate manual review artifact. Replace its non-final job name and image,
  provide a distinct least-privilege migration secret, verify backup/rollback
  and schema compatibility, and obtain the user's per-step confirmation before
  an eventual production migration. The migrator already caps its pool at two,
  lock timeout at three seconds and statement timeout at 120 seconds.

## Resource and database budget

Initial values are assumptions for testing, not a qualified user count. API pods
request 250m CPU/256Mi RAM, limit 1 CPU/512Mi RAM, and have a bounded temporary
filesystem. Media/ffmpeg memory, subprocess concurrency, temporary storage and
timeouts still need their own workload tests. The read-only root filesystem must
be tested with actual upload paths; do not make broad write permissions a fix.

HPA is limited to two through three APIs, one additional pod per minute, with a
five-minute scale-down stabilization. It requires functioning CPU metrics. It
adds pods on existing capacity, not VPS machines. The API's four-connection pool
and disabled separate read URL mean at most 16 API connections during a
three-replica rollout with one surge, before workers/tools/operational reserve.
A future one-pod notification worker adds four. Include omitted roles,
monitoring, migration, backups and admin tools in the real connection calculation.
Do not add this budget to the historical production budget of 84/100 and assume
parallel cutover is safe. Staging has its own database, and production budgets
must be recalculated from a fresh inventory.

## Networking, secrets and availability

The namespace defaults to denied ingress/egress. Explicit rules allow only DNS,
placeholder private PostgreSQL/Redis and same-namespace mock SMTP/push traffic.
Review DNS/Traefik labels and actual VPN/CNI routes; verify enforcement with
positive and negative probes. Do not open PostgreSQL/Redis or overlay/etcd ports
to the public Internet. No public Google, media-storage, billing or Expo access
is enabled by these rules; those flows remain excluded until isolated transport
and approved restricted egress are established.

Create runtime/TLS/migration secrets through a reviewed secure mechanism outside
Git, encrypt secrets at rest on the cluster, restrict RBAC, and test rotation.
There is no placeholder password or copied production `.env` in this directory.
Do not log secret objects or connection strings in deployment evidence.

Preferred API anti-affinity permits a one-node staging demonstration. It does
not provide host HA. For the proposed three-node application cluster, validate
hard hostname separation, spare rollout capacity and tested node failure before
claiming application resilience. The PodDisruptionBudget governs voluntary
disruption, not arbitrary machine loss. PostgreSQL, Redis and external ingress
failover are independent designs. Keep production PostgreSQL outside the initial
application cluster. Do not install K3s beside live Coolify without a reviewed
port, routing, firewall, resource and rollback plan.

## Acceptance still required

1. Pin the K3s and image versions; verify OS, CPU/RAM/disk/I/O, available capacity,
   private addresses, trust boundaries, metrics and node failure domains.
2. Validate against the target Kubernetes API, then start only the isolated
   environment after source/configuration and mock-egress review.
3. Test startup, readiness/liveness, draining and rolling deployment; killed API
   and failed-node recovery; secret rotation; DNS/Redis/database outage behavior.
4. Qualify full worker entrypoints/roles, leases and receipt throughput with
   controlled mock failures. Preserve shared authentication, message ordering,
   authorization, Town Hall limits and driver expiry across instances.
5. Observe per-operation p95/p99/errors and pool waits; PostgreSQL locks/I/O;
   Redis eviction/recovery; notification queue age and eligible receipt backlog;
   CPU/memory/disk saturation; ingress/certificate health and external backup
   age. Prove alerts can fire and reach an operator using a reviewed test route.
6. Restore a backup into isolated storage, measure data-loss/recovery bounds,
   rehearse the complete approved cutover and rollback, then report a launch
   decision supported by the measured workload and explicit exclusions.

No installation or apply commands are included because this is preparation,
and the current approved priority is application qualification.
