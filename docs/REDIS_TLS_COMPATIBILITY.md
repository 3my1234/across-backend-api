# Redis TLS compatibility and controlled production proposal

Prepared 22 October 2026. Recovery gate is closed separately; no production
Redis/API/worker/controller configuration is changed by this work. PR #66 stays
unmerged. The CI lab uses only disposable certificates, credentials and data.

## Verified starting point

Operator evidence: Redis 7.2.16, port 6379, tls-port 0, protected-mode no,
tls-auth-clients yes; password configured in application clients. Docker reports
no Redis host port publication. This does not prove firewall policy, ACL scope,
persistence or all client identities. Existing DB inter-host transport is
WireGuard, independently verified; preserve it.

Subsequent operator inventory at 2026-10-22 14:29:50 CEST identifies the Redis
image ID as `sha256:0637954999d01b7c9ce9167db2da50656e2590d3b884f1c600c5f63bb6e6773c`.
The only reported mount is named volume
`redis-data-rxys19f7r5rc8g7cmlb9wkg6`, mounted read/write at `/data` from
`/var/lib/docker/volumes/redis-data-rxys19f7r5rc8g7cmlb9wkg6/_data`.
Preserve that exact volume; never initialize a replacement empty volume.
Reported settings: appendonly=yes, appendfsync=everysec,
save=`3600 1 300 100 60 10000`, maxmemory=0, maxmemory-policy=noeviction.
The first lab used temporary storage; the follow-up uses owned named volumes
and these persistence settings to test graceful replacement and old-startup
rollback. It does not establish crash-loss tolerance or real-data durability.
maxmemory=0 is not a bounded memory budget: review growth/host limits separately;
do not change eviction or persistence policy incidentally during TLS work.

The proposed mode is server-authenticated TLS plus the existing Redis password
(or separately reviewed ACL credentials), with `tls-auth-clients no`. This means
client *certificates* are not required; Redis password authentication remains
required. The old dormant `tls-auth-clients yes` would otherwise require mTLS.
No new Redis proxy or server is proposed.

## Candidate changes and isolated evidence

`Config.RedisOptions` is used by API/worker shared `db.New` and production/staging
preflight. `REDIS_TLS_CA_FILE` optionally selects an explicit PEM trust bundle
(maximum 1 MiB); absent it, the system trust store is used. TLS requires verified
certificate identity, hostname from REDIS_URL and minimum TLS 1.2. There is no
skip-verification or hostname-override setting. A CA file with plaintext Redis
is rejected; errors omit credentials and file contents. No private key belongs
in the application CA bundle.

The `redis-tls` GitHub job uses the observed Redis 7.2.16 version and disposable
PostgreSQL 16 on an isolated CI runner. It exercises:

- Additive TLS listener activation while a legacy listener stays usable.
- Trusted TLS/password success; untrusted CA, mismatched hostname, expired
  certificate, absent password and wrong password rejection.
- Independent API/worker shared Store constructors, synthetic key exchange and
  Pub/Sub. No live jobs, notifications, payments, emails or production DB access.
- TLS-only listener cutover and legacy-listener rollback preserving a test key.
- Graceful container replacement from durable TLS-only startup configuration,
  retaining an owned named volume/AOF and test key, then replacement using the
  old plaintext startup configuration with the same volume and retained key.
- New candidate preflight still rejects off-loopback plaintext rollback config.
- Owned Redis test-container cleanup, with job assertion that none remain.

Full API/worker entrypoint startup, Docker image mounts/non-root permissions,
real job semantics, Coolify restarts, real data persistence and end-to-end
release rollback remain staging gates. The transport test uses AppEnv=test
against disposable loopback PostgreSQL; it is not production-config proof.
Certificates/keys are synthetic runner-temporary files, not uploaded artifacts.
Only the Go test log is retained. No production load test is performed.
The lab asserts both owned containers and owned named volumes were removed.

## Exact intended configuration shape (NOT applied)

1. Retain the current Redis data volume, exact image identity, authentication,
   database selection, eviction and persistence policy. Record sanitized current
   mounts/persistence/ACL/client inventory before approving a change. Never print
   passwords, Redis values or raw configuration files.
2. Install a dedicated server certificate/key and trusted CA. Its SAN must match
   a stable internal Docker DNS alias selected for Redis. Do not pin a changing
   container IP. Keep private CA signing key off service containers. Redis gets
   only its leaf key/certificate and public CA bundle, with Redis-user-readable
   restricted key permissions. Record expiry and renewal/rotation ownership.
3. Add TLS on internal port 6380 while retaining existing TCP 6379 during the
   migration. Intended Redis settings:

   ```text
   port 6379
   tls-port 6380
   tls-cert-file /run/atl-redis-tls/server.pem
   tls-key-file /run/atl-redis-tls/server.key
   tls-ca-cert-file /run/atl-redis-tls/ca.pem
   tls-auth-clients no
   tls-protocols "TLSv1.2 TLSv1.3"
   ```

   Existing password/ACL remains configured. No host publication for either
   port. Do not change `protected-mode` or ACLs incidentally during this cutover.
   In the lab port numbers are inverted (TLS6379/legacy6380); production keeps
   the current legacy port unchanged.
4. Make the public CA bundle available read-only at
   `/etc/atl/redis/ca.pem` in original API, worker AND both replica images or
   explicitly reviewed mounts. Then set the candidate shape:

   ```text
   REDIS_URL=rediss://<existing-user>:<URL-encoded-existing-password>@<verified-internal-DNS-alias>:6380/<existing-db>
   REDIS_TLS_CA_FILE=/etc/atl/redis/ca.pem
   REDIS_OPTIONAL=false
   ```

   This is a template, not a deployable secret-filled command. REDIS_URL takes
   precedence over REDIS_ADDR/PASSWORD/DB, so preserve its user, password and DB.
   Existing replica Compose does not inherit Coolify mounts. Prefer delivering
   the reviewed public CA at the same path inside both immutable service images
   (replicas use the API image), or explicitly qualify a Compose/controller
   mount change. Merely copying environment values is insufficient.
   Candidate controller now compares REDIS_TLS_CA_FILE between API and worker;
   path equality does not establish matching file bytes. Compare CA fingerprints
   and readability as uid 10001 on every role in staging.
5. After all consumers use verified TLS and observation confirms the migration,
   disable plaintext with `port 0`. Persist approved listener settings in the
   actual Coolify-managed startup configuration; runtime CONFIG SET alone is
   not durable. Rehearse restart/recreation with the same data volume in staging.

## Availability, approval and rollback

The lab tests in-process listener changes without a Redis restart. This does
**not** establish a zero-downtime production change: adding certificate mounts
to the current Docker container may require recreation, and current Coolify
startup/persistence details are not fully collected. A single Redis instance
can have a connection interruption. No defensible duration is available yet;
measure it in staging before scheduling the production maintenance window.

The existing `/data` volume also permits a reviewed certificate/configuration
subdirectory without adding another Docker mount, but files/runtime CONFIG SET
alone do not change Coolify's startup command. Do not adopt this route unless
startup/permissions/secret retention and persistence across managed recreation
are qualified. The synthetic graceful-replacement duration excludes certificate
preparation, application rollouts, real AOF loading and Coolify scheduling, and
must not be presented as a production downtime promise. Existing plaintext
connections can survive listener closure; confirm every client has moved to TLS
rather than equating `port 0` with termination of all old sessions.

Before any authorized production release: record immutable old API/worker/Redis
images, sanitized startup configuration, certificate fingerprints, data-volume
identity and restricted environment snapshots. Stop only future replica timer
runs and let any active reconciliation finish, under separate approval. Do not
use the controller's `--pause` command as an automation-only gate: it reroutes
traffic. Keep original/replica release identity coordinated.

While legacy TCP remains, rollback means deliberately returning the old API,
worker and replica release together to their recorded settings, retaining the
same Redis data. If plaintext was disabled, first re-enable the original 6379
listener using an approved authenticated TLS administration channel and restore
the durable old listener configuration. Never flush Redis or replace its volume.
Do not weaken the candidate validator; the old release is the rollback target.
If Redis cannot start, restore the recorded Redis startup/image with the same
volume, then verify authentication/readiness and queued-job recovery. Persistence
and worker retry/duplication behavior require staging rehearsal before approval.

Approval to implement is intentionally not requested until the isolated results
and remaining concrete production configuration choices are ready for review.

## Development-login exposure while release is pending

Production's reported 57411f0 predates the handler guard. Source review shows
`POST /api/v1/dev/login` is not adequately gated by that release's env flag.
The candidate rejects the route before DB access in production/staging, with
regression coverage. No exploit or live POST has been attempted.

Treat temporary containment as high priority before broader launch: prepare an
exact normalized-path deny at the actual ingress, covering every externally
reachable API path, or a separately reviewed minimal guard backport. A Cloudflare
rule alone is insufficient unless origin bypass is also excluded. Do not merge
all of PR66 just to get the guard, and do not assume flipping ENABLE_DEV_LOGIN
fixes the old handler. Any ingress edit or hotfix release requires the user's
separate authorization and harmless staging verification first. No protection
is claimed deployed.

## Following gates

Qualify socket peers/XFF sanitization through actual Traefik + HAProxy topology,
then explicit trusted CIDRs; prepare exact DB_VERIFIED_VPN_HOST=10.77.0.2 while
preserving independently verified tunnel routing; verify authenticated admin
access before removing bootstrap settings; coordinate separate migration command
and Coolify pre-deployment hook; finish staged API/worker/replica rollback and
external alert acceptance. No merge/deploy until these gates are satisfied.

Configuration semantics: https://redis.io/docs/latest/operate/oss_and_stack/management/security/encryption/
