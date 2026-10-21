# Milestone 1 operations and evidence

Repository implementation does not authorize deployment. This candidate deliberately rejects insecure production settings. Review all prerequisites in staging before approving a production release. No database migrations are added.

## Production prerequisites

Both production images set `APP_ENV=production`, `RUN_INLINE_WORKERS=false` and `RUN_MIGRATIONS=false`. API and worker validate configuration before connecting. Use the separate migration executable only in an explicitly authorized release step. Development fixtures must explicitly use development/test environments; `ENABLE_DEV_LOGIN` is false by default and cannot operate in staging/production, even if enabled accidentally.

Preserve the existing securely generated signing secret; do not rotate it to satisfy a preflight by accident. `JWT_SECRET` must be at least 32 bytes; bootstrap admin tokens and mock payments must be disabled. Use authenticated admin accounts. Validation errors name fields and never echo their values.

Database, read-replica and migration connections must use `sslmode=verify-full` with the correct hostname and CA. Plaintext or downgrade-capable connections fail production/staging preflight. Redis must have authentication and verified TLS (`rediss://`) unless it is accessed on loopback. A private Docker DNS name is not loopback. This can require configuring transport before rollout; do not merely enable insecure certificate bypasses. Network reachability/firewall/private-network evidence is still required separately.

`TRUSTED_PROXY_CIDRS` must contain only the verified direct proxy and upstream proxy addresses/networks that append or overwrite X-Forwarded-For correctly. Universal networks are rejected. Inventory Traefik, HAProxy and any Cloudflare path first; never copy broad example Docker/private ranges blindly. Rate limiting uses the rightmost untrusted XFF hop only when the direct peer is trusted. Without a trusted chain it uses the socket peer, which can group users behind a proxy. Test this in staging to avoid putting all customers in one rate-limit bucket. CF-Connecting-IP/X-Real-IP are not independent proof of identity. Unverified CF-IPCountry is ignored; geographic hints remain a product convenience, not a security boundary.

Require exact HTTPS CORS origins and a bounded HTTP/database pool. Budget all API, worker, replica, migration, monitoring and deployment-surge connections together. New pool validation is not a capacity guarantee.

## Offline preflight and version evidence

Candidate images contain `/app/across-config-check`. Run it with the intended environment in an isolated candidate/staging container before deployment:

```sh
/app/across-config-check --role api --expect-env production
/app/across-config-check --role worker --expect-env production
/app/across-config-check --role migrate --expect-env production
/app/across-config-check --build-info
```

The first three load configuration but never connect to PostgreSQL/Redis or call external services. Use the appropriate role per container. Do not paste environment dumps or connection strings. Build identity mode does not load `.env` and returns only service, revision, modified and Go version. Image builders must pass `--build-arg SOURCE_COMMIT=<full-tested-commit>`; `unknown` is an evidence gap, not a passing version check. CI tests injection. The API and worker log build identity at startup; worker liveness includes it too. Fresh operator evidence must include both replicas, not only the original API.

The super-admin-protected `GET /api/v1/admin/ops/runtime` exposes per-process request totals, in-flight requests, status classes, fixed latency buckets and database pool counters. Its histogram is disjoint buckets, not Prometheus cumulative buckets or an exact percentile. Counters reset when the process restarts and must be collected from each API replica independently. Private responses are no-store; no raw paths, message bodies, IP labels or tokens are collected. Existing `GET /api/v1/admin/ops/queue-health` remains necessary for durable job backlog. A healthy worker heartbeat does not prove successful delivery.

External monitoring must collect signals and deliver alerts outside the application host. Configure the destination/recipients and prove alert delivery separately. Required initial signals: public availability and latency, 5xx/429/503 rates, deployment version mismatch, database pool pressure, queue age/failure, disk/memory pressure and restart/OOM. Define thresholds against observed staging baselines; new counters alone do not constitute an alerting system.

## Isolated application backup restore

Current evidence: operator supplied Coolify `across_db` archive success with Local Available/S3 Available. The latest pasted archive was 468.49 KB and completed 33 minutes before the page was copied. Exact completion timestamp and successful restoration remain unverified.

Before a real-archive drill, obtain explicit approval for downloading/handling that private backup in the chosen isolated environment. Use a trusted machine, a protected local file and the matching/newer supported PostgreSQL major. Confirm archive download availability and retention independently. Record the absolute UTC completion time, object/checksum and recovery objectives; never paste private database contents.

`scripts/restore-drill.py` accepts a local custom-format PGDMP archive only. It has no production connection/hostname argument. Preview hashes the file but creates no container. Execution uses a fresh randomly named, labelled disposable container with no network, no published ports, no host filesystem mounts, an unprivileged user, dropped capabilities, bounded CPU/RAM/PIDs and temporary database storage. The source archive is streamed, preserved, and not mounted. The image must already be present locally; no implicit pulls occur.

Illustrative commands for the approved isolated machine (not the live database container):

```sh
python3 scripts/restore-drill.py --archive /protected/backup.dmp --postgres-major 16
python3 scripts/restore-drill.py --archive /protected/backup.dmp --postgres-major 16 --execute
```

Choose the major from source evidence; 16 above is an example. Limits are intentionally small (512 MiB temporary PGDATA/1 GiB RAM) for the present small archive and CI fixture. A larger database needs a reviewed isolated resource plan before running. A custom archive can execute SQL during restore, so the no-network/no-host-mount sandbox is essential; do not restore untrusted files directly on a personal or production PostgreSQL server.

The tool checks restore exit status, core table readability/counts and validated constraints, prints only checksum/aggregate evidence, then removes only its own verified isolated container/volumes. It does not start application workers, email, push or payment processing. A failure is not a successful recovery. Confirm cleanup if Docker itself becomes unavailable.

CI restores a synthetic archive and verifies exact synthetic counts. That establishes tooling behavior only. A real archive additionally requires schema/migration coverage, representative listings/chat/subscription/payment relationships, reconciliation against evidence associated with the archive snapshot, and application read checks with outbound delivery/payment calls disabled. Record restore duration, recovery point, checks performed and pass/fail results. Do not expose personal records in evidence. This drill does not establish standby failover, point-in-time recovery, restore with original ownership/privileges, or production RPO/RTO.

Never click Import Backup on the live database for this test. Never run the drill against `across_db`; the tool uses only its disposable `recovery_drill` database.

## Rollout and rollback

Review PR/CI evidence first; approve staging configuration and candidate execution next. Correct transport/proxy configuration before production. Preserve the known-good API/worker images and deployment settings; do not apply the candidate until its preflight and staging checks pass. Deploy API/worker at matching revisions and verify both replica identities and readiness. Roll back to the known-good images/configuration if the new release fails; no schema rollback is needed for this change. A rollback that restores an exposed development endpoint is not acceptable as a permanent state: block the route at ingress while arranging a safe corrected release.
