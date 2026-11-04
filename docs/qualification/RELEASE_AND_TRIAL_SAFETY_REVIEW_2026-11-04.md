# Release and isolated trial safety review, 4 November 2026

**Decision: ready to request authorization for the isolated staged application trial. Production deployment is not authorized or certified.** No VPS contact, setup, production DDL, live load, DNS, Coolify or K3s change occurred during this review.

## Release source and verification

GitHub main remains `30b0b68409f5dc12e9f63f332815d35acb1d3f08`. Reconciled candidate now **`90b905660cad8896aacff74242f920846abc33a3`**, branch `experiment/release-reconciliation`. The additional commit contains only a migration regression test and its disposable CI gate. Runtime Go source, API/worker entrypoints and migration SQL remain equal to qualification image source `13d316c62ce8bdd35c1c2fe72bf3d4d357581914`. The only application-tree additions relative to that source are the polling and migration-review tests. No newer main changes were overwritten; ordinary backend user modifications remain untouched.

[Release and rollback validation41875176028](https://github.com/3my1234/across-backend-api/actions/runs/41875176028) **SUCCESS** at candidate90b9056. Seven Go packages ran against disposable PostgreSQL, seven named critical database regressions were required to pass, and all16 polling measurement records passed. Earlier source/polling proof41871729928 remains retained; current tests add migration108-specific evidence rather than replacing correctness gates.

## Migration108 review

Files: `migrations/108_push_receipt_claims.sql`, `internal/migrations/runner.go`, `internal/db/db.go`, `cmd/migrate/main.go`, new `internal/migrations/receipt_release_test.go`.

- Adds four receipt scheduling/lease columns, one partial receipt index and one singleton dispatch table. No existing columns, tables or business rows are removed; legacy explicit-column receipt SQL remains compatible.
- `ALTER TABLE` needs an exclusive table lock. Regular `CREATE INDEX` blocks concurrent writes to this table until transaction completion; it is not a concurrent index build. Additive does **not** mean nonblocking. Production data volume and actual lock/build duration are not established by disposable tests.
- The existing runner wraps the entire migration and its `schema_migrations.name` ledger insert in one transaction. A session advisory lock serializes migration passes. Migration108 is not raw-SQL idempotent; reruns must use the ledger-aware runner, not execute the file twice manually.
- Existing dedicated `cmd/migrate` uses `NewMigrationPool`: session-only lock timeout3s, statement timeout120s, idle-transaction timeout60s, connection timeout5s; overall command context3min. Use this controlled path for an approved release. Ordinary API startup migration uses its ordinary pool, so keep `RUN_MIGRATIONS=false` on deployed API replicas and run the separate approved migrator once.
- Focused actual-PostgreSQL test proved bounded lock refusal (55P03), a forced index-name collision after ALTER (42P07), no partial columns/ledger after failure, successful retry with defaults and singleton gate, and previous receipt worker SELECT/UPDATE compatibility. Tests use only a uniquely named loopback disposable schema and clean it afterward. They do not prove full old/new worker overlap safe.
- All migrations through108 also passed the full disposable seed rehearsal in41872754153.

Local migration package review encountered an existing Windows CRLF-sensitive assertion in migration032's test. The new test compiled locally but its opt-in database execution was skipped there; real execution and the complete package passed on Linux CI. No production SQL or unrelated runtime changes were made to address that platform-specific test limitation.

## Deployment and rollback conditions

Production release remains a separate approval. Before that approval, capture actual deployed API/worker image digests, effective roles/inline-worker settings and schema ledger; retain the exact prior images/configuration and approved backup/restore evidence. The October audit is historical, not a current health assertion.

Receipt worker versions must not overlap during upgrade **or rollback**. The old implementation does not honor the new claim/dispatch lease. Account for dedicated worker containers and any API with inline workers. Stop/drain old receipt consumers before starting new ones; reverse this order for rollback. Do not claim a rolling mixed-worker deployment is protected by the new lease.

If migration fails, stop the release and confirm transaction/ledger state; do not mark it applied manually or increase timeouts blindly. If application rollback is necessary after a successful migration, drain new receipt consumers, restore the recorded prior application/worker images and configuration, and leave additive migration108 in place. Do not drop receipt columns/index/table or restore the whole production database as routine code rollback. Verify readiness, business operations, queue state and receipt processing after the change. Stop and seek a separately scoped repair if data inconsistency is found.

These production checks do not block testing an isolated synthetic database: the proposed trial uses only its own schema/data, and does not migrate production.

## Concrete isolated trial prepared for authorization

Fresh local review **`cohost-fbddeed5c9cfc458`**; no server resources created and no GitHub credential uploaded. Both role setup/image-import/load flags are **false**, and fixture preparation is **false**. Old closed nonce e859 must not be reused.

Coordinator source **`836b8d671da0190bc094c2fadb5b6c0f0de394e8`**, retained in clean detached `.worktrees/trial-execution-reviewed`. [Exact-source safety run41872754153](https://github.com/3my1234/across-backend-api/actions/runs/41872754153) succeeded: Linux84 tests, image archive identity, watchdog stop, exact-owned cleanup, restricted SSH/PostgreSQL transport and full500-pair seed with real loopback relay. Host sensors in that rehearsal were simulated; actual cross-VPS transport remains a runtime prerequisite, not an offline guarantee.

Application image remains the already-retained immutable13d316c image; candidate runtime equality checked. Existing image archives are reused after SHA256 verification. No repeated199MB upload, new image pull, public database exposure or production endpoint load is intended. Historical generator proof is checked locally against retained original hashes before SSH; no historical artifact download prerequisite.

| Resource/stage | Reviewed limit |
|---|---|
| Main test slice |2CPU maximum aggregate;2.5GiB RAM; no swap |
| Isolated test PostgreSQL |1CPU idle setup,3CPU only after separate trial activation;3GiB RAM; no swap |
| Free GitHub generator |2CPU /4GiB; original headroom gates |
| Small baseline |20 simultaneous sessions /45seconds |
| Intermediate |100 /180seconds |
| Gradual stage |500 /180seconds |
| Sustained stage |500 /1800seconds, original mixed workload and fault/integrity checks |
| After workload |Original send drain5minutes maximum and receipt tail20minutes maximum |
| Overall |Expected roughly60-90minutes including two five-minute baselines, seeding/setup and drain; independent trial lifetime120minutes. Interactive SSH/queueing delays can add wall time; activation expiry checks remain |

CPU quotas are caps on shared hosts, not dedicated core reservations. Existing services remain protected by unchanged watchdogs: available RAM>=2GiB; disk reserve30GiB main/20GiB database; stop on OOM or unhealthy protected containers, CPU>85% for5seconds, iowait>10% for5seconds, two consecutive unhealthy public probes, three consecutive latency breaches relative to protected baseline, stale peer lease>5seconds, malformed/missing observations or lifetime expiry. HTTP2seconds/observer2.5seconds/status3seconds remain unchanged. Baselines and runtime public probes remain required. All application latency/error, queue-drain, acknowledgement, retry/idempotency and reconciliation requirements remain unchanged.

Existing exact-owned cleanup runs on success/failure/interruption and independent watchdog/reaper expiry; only manifest-recorded immutable IDs with the run's ownership label may be mutated. Restricted temporary key lines and trial GitHub secret are removed; unrelated containers/volumes/keys are not cleanup targets. Evidence retrieval failure is not proof cleanup succeeded; retained cleanup markers and current status must be examined after execution. No guarantee is made that a future runtime baseline or workload will pass.

Operator script `qualification/Invoke-CohostQualification.ps1` has been regenerated for this fresh review and exact passed source, replacing its obsolete closed-run reference. PowerShell AST passed. Both roles' copied Python tools match reviewed source byte-for-byte; fixed trial policy validated in memory without changing any approval file. SHA256 and review identity recorded in `qualification/FINAL_RELEASE_REVIEW_VALIDATION_2026-11-04.json` (operator SHA256 `714a42a0e51dfbe9df0eddb4dc9bc97bb3422f6920a5acb40ad5f0f7bf2a1201`).

**Approval required before running the operator or creating live-host resources.** The requested approval covers this isolated trial only, not production deployment, production migration108, purchases or K3s. No new password/audit command is needed before that decision. If approved later, refresh/validate local package expiry through the reviewed operator; refuse source/identity/approval mismatch, do not reuse closed resources or bypass baseline failures.
