# Launch capacity and security evidence, 22 October 2026

Offline review at foundation `be1336c`. No production requests, load tests,
deployments or configuration changes were made. Registered users, monthly
active users, simultaneous sessions, requests in flight and requests/second
are distinct measurements. No million-user capacity has been established.

## Current architecture and limits

Operator evidence establishes a main application/Redis/Coolify host and a
dedicated PostgreSQL host. PostgreSQL binds to WireGuard address `10.77.0.2`;
the peer route uses `wg-atl`. Public traffic has Cloudflare/Traefik and an
HAProxy gateway serving two API containers; original API and worker are separate.
Two API containers on one host provide process redundancy, not host redundancy.
Redis and PostgreSQL are each single failure domains. The historical 12 October
scaling roadmap predates this database split and the completed real restore.
Its one-host topology and migration instructions are not current instructions.

| Protection | Source | Boundary |
|---|---|---|
| API admission, JSON busy response, Retry-After, drain | `internal/middleware/admission.go` | Bounds active work; does not add CPU/database capacity |
| Replica pools and deployment surge budget | `scripts/replica-pilot.py`, `scripts/replica-release.py` | Release alignment, not infrastructure autoscaling; count every consumer |
| Shared database/Redis and optional read pools | `internal/config/`, `cmd/api/`, `cmd/worker/` | More replicas/pools consume additional connections |
| Nine durable worker loops share a worker pool | `internal/workers/workers.go` | Queue-specific backlog can be hidden by overall process health |
| Ordinary chat and Town Hall share API/database | `internal/controllers/`, `internal/workers/town_hall.go` | Shared CPU, pools, locks and I/O can transmit overload |
| Isolated capacity and distinct-session labs | `deploy/capacity-lab/`, `deploy/session-lab/`, `cmd/capacity-lab/` | CI software baseline, not measured production-host capacity |

## Measured evidence and untested targets

`LAUNCH_MEDIA_POLLING_RESULTS_2026-10-20.md` records four VUs for 45 seconds:
API successful p95/p99 26.71/182.29ms, polling observation p95/p99 14.28/14.54s,
ten of ten marketplace messages observed. Six sequential photo roundtrips
matched hashes against an isolated TLS S3-compatible service. These are small
functional checks; they do not qualify concurrent media or physical phones.
The real database restore and isolated Redis TLS/authentication tests passed
their documented scopes. Neither proves numerical user capacity.

The existing session lab already implements 100, 500, 1,000, 2,500, 5,000,
10,000 and 12,000 session stages, distinct synthetic identities, real login,
thought time, independent 15-second history polling and write reconciliation.
Burst arrivals are separately modeled and cannot advance sustainable stages.
Only smoke is exposed automatically in CI; larger stages require current
explicit isolated-environment/session/duration/spending approval.

This change tightens the existing gates: missing/non-numeric maximum approvals
and invalid fixture expiration fail closed. Advancement must preserve the entire
reviewed session model, so deleting its polling interval cannot bypass polling
latency gates. Missing achieved-VU evidence is rejected. Pure Node policy tests
passed; no larger traffic stage was run or authorized.

At 100/500/1,000/5,000/10,000 sessions the model's conversation polling alone
implies approximately 6.7/33.3/66.7/333.3/666.7 requests/second, before browsing,
sends, inbox refresh and retries. This arithmetic explains the distinction;
it is not a measured workload or supported capacity claim.

Review stages independently using per-operation successful p95 <500ms/p99
<1,500ms, unexpected failures <1%, zero ordinary-stage overload and all identity/
content checks. Require achieved VUs, no generator drops and confirmed/stored
write reconciliation. Collect CPU/RAM/I/O, DB pool waits/locks, Redis latency/
memory and worker oldest-job age. Stop if correctness fails, queue age/pool waits
keep growing, memory approaches its limit, or errors/generator drops occur.
Never change thresholds retrospectively to pass a failed run. Existing
`deploy/session-lab/README.md` describes execution, cleanup and limitations.

## Economical growth plan and investigation triggers

| Stage/trigger | Next action to qualify | Why |
|---|---|---|
| Current two hosts | Complete compatibility, external alerts, rollback and synthetic capacity evidence | Avoid spending based only on account counts |
| App CPU >70% for 10min with latency pressure and healthy DB/Redis; or host-loss availability requirement | Second app host and tested load-balancer failure path, recount pools/surge | Current two replicas cannot survive main-host loss |
| Oldest queued job >30–60s and rising | Queue-specific concurrency/vendor-limit tests; separate worker deployments by job family if needed | Isolates work without extracting all of Town Hall |
| Pool exhaustion, lock waits, query/I/O pressure | Index/query/transaction work before pooler/read replicas or DB enlargement | More API replicas or max_connections do not fix DB saturation |
| Redis memory/latency/reconnect pressure | Measured memory budget and recovery qualification | Live maxmemory=0 means no Redis memory ceiling; noeviction alone is not a cap |
| Sustained domain-specific load after optimization | Independently deploy real-time/Town Hall component only if evidence justifies ownership and isolation | A service split sharing a saturated DB preserves its bottleneck |

Keep the modular Go backend initially. Proposed alerts include unexpected
5xx >1%/5min, successful p95 >500ms/10min, available RAM <1.5GiB, disk >70%,
pool waits, missing backups and increasing queue age. These are starting policy
triggers, not observed capacity boundaries. External alert delivery still needs
qualification. No new server, provider pricing or paid provisioning is required
to implement these offline safeguards; extra hosts depend on measurements and
the required availability, not a promised account count.

## Development-login containment

Deployed `57411f0` enters development-login database mutations/token creation
without the new production guard. Do not probe it live. The separately prepared
minimal patch at the same base adds opt-in `ENABLE_DEV_LOGIN=false` and rejects
production/staging/empty/unknown environments before database access, without
the larger foundation configuration changes.

Make a separate containment decision: a narrowly scoped ingress deny for POST
`/api/v1/dev/login`, or a coordinated minimal-patch API/replica release. The
latter must account for worker/controller revision alignment. Neither has been
applied. Do not assume compromise or rotate all session secrets solely from this
finding; any incident metadata review needs its own safe scope.

## Remaining launch gates

- Actual Redis TLS, trusted-proxy chain, VPN pin, bootstrap and migration controls.
- API/worker/replica compatibility, single migration ownership and rollback.
- Separate approved development-login containment.
- Controlled synthetic staging stages and external generator/resource evidence
  before numerical concurrency claims; no production load tests.
- Android/iOS messaging, deep links, media and native-provider parity checks.
- Delivered alerts, media restoration and host-loss recovery tracked separately
  from the completed database-archive restore.
