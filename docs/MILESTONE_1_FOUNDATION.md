# Milestone 1: secure and observable foundation

Status: implementation in progress; production gates are not satisfied by a source change.
Started: 21 October 2026; continued 22 October 2026 (Africa/Lagos).

## Scope and dependency map

```text
configuration preflight -> API/worker startup -> database/Redis connections
verified proxy topology -> trusted client-IP handling -> rate limiting/location lookup
toolchain/dependency updates -> unit/race/integration/image/vulnerability checks
request metrics + build identity -> deployment evidence + external monitoring
S3 archive availability -> isolated restore -> data verification -> recovery evidence
all evidence reviewed -> milestone acceptance -> native provider implementation
```

No production deployment, migration, restart, firewall/DNS change or restore is authorized by this checklist. No production load tests. Repository changes will remain reviewable on a branch/PR.

## Implementation checklist

| Work | Exact source/configuration area | Acceptance evidence |
|---|---|---|
| Fail closed on unsafe production/staging settings | `internal/config`, API/worker entry points, new offline configuration-check command | Tests reject unsafe secrets/bootstrap/mock payment and insecure transport without logging credential values; accept only verified TLS or an independently verified, explicitly pinned private VPN peer; check works without connecting to dependencies |
| Disable development login by default | `internal/controllers/dev_controller.go`, configuration and security tests | No environment can use demo login without explicit development/test opt-in; production/staging reject before database access |
| Explicit trusted proxy handling | `internal/middleware/scale.go`, `country.go`, new client-IP middleware and API wiring | Direct spoofed forwarding headers ignored; trusted chain resolved from right to left; malformed/untrusted chains cannot choose arbitrary client IP |
| Dependency maintenance | `go.mod`, `go.sum`, Dockerfiles, CI workflows | Supported Go image and CI toolchain; reachable vulnerability scan, full tests and Linux builds; unresolved findings explicitly reported |
| Bounded observability | New request aggregate middleware and protected runtime endpoint; API/worker build identity | Counts include overload/rate-limit rejection and panic/handler errors; fixed-size aggregation; no query parameters, tokens, message contents or raw paths in metrics |
| Release evidence | Image build metadata and offline/inside-container inspection command | Operator can compare actual API, worker and replicas with expected revision; cannot mistake an image label for fresh runtime confirmation |
| Recovery qualification | Isolated restore runbook/check script and disposable CI recovery test | Archive checksum, restore success, schema/data invariants and measured elapsed time; production archive restore remains a separate explicitly authorized isolated operation |
| Production handoff | This checklist and final evidence report | Required settings and rollback documented; no claim that local tests establish live security, working alerts or recoverability |

## Provider migration contract (Milestone 2)

Preserve the existing provider functions, information architecture and latest UX/UI designs in native React Native screens. Move the experience into the shared mobile application without changing existing account ownership, paid access or provider data. Customer, applicant, approved/suspended provider and dual-role capabilities come from backend authorization. Full route/action parity is required before retiring the PWA; the internal web admin is separate. Provider subscription expiry and access to historical conversations require explicit policy/regression coverage, not an incidental side effect of navigation changes.

## External evidence still required

- Fresh API/worker/replica versions and production configuration preflight results, with no credentials pasted.
- Actual ingress addresses/header rewriting, origin access restrictions and database/Redis network/TLS/firewall evidence.
- Current backup absolute completion time, retention and verified object download; successful restore of that archive into an isolated environment with outbound jobs disabled.
- Monitoring destination/alert recipient configuration and a demonstrated test alert; no external message is sent by repository tests.

## Rollback and release

Do not deploy until offline configuration preflight and staging tests pass. New startup validation may intentionally reject old insecure settings; correct and stage them before a production cutover. Retain the known-good image and explicit rollback procedure. Do not rotate signing secrets casually: preserve existing sessions during this milestone. No database migration is planned for this foundation change. Restore tests create disposable databases only and must never target `across_db`.
