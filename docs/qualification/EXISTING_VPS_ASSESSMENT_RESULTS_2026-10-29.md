# Existing VPS assessment and proposed bounded trial

29 October 2026. Status: assessment complete; co-hosted setup/load NOT executed. No purchases.

Latest capture: C:\Users\EMMA\Desktop\gouwu\qualification\existing-vps-assessment-20261029\20261029T154311Z

| Host | CPU p50 / p95 / maximum | Available RAM | Free disk |
| --- | --- | --- | --- |
| main (4 vCPU) | 13.21% / 26.32% / 88.46% | 6.28 GiB | 128.77 GiB |
| database (4 vCPU) | 3.74% / 5.63% / 8.85% | 7.07 GiB | 90.90 GiB |

Both reports completed without errors. No SQL, stress, settings changes or service restarts occurred. These are short passive samples, not peak-capacity guarantees. The latest main-host sample briefly reached 88.46% total CPU: a monitored pilot is justified, but unrestricted simultaneous load is not. Earlier samples must not replace the latest capture.

## Proposed ceilings and gates

- Free GitHub Actions: retain qualified generator at 2 CPU / 4 GiB; no generator on either production VPS.
- Main: NEW test-only parent resource group capped at 2 CPU / 2.5 GiB, no swap, reduced CPU/IO weight. All test APIs, gateway, workers, Redis and mocks must belong to this group; verify effective ancestry before seeding. Keep existing per-service limits, pool sizes and admission controls. Shared aggregate limits can throttle the test and must be reported.
- Database host: NEW isolated PostgreSQL container/process/volume, initially 1 CPU for connectivity checks, maximum 3 CPU / 3 GiB for approved trial, no swap and lower CPU/IO weight. Production PostgreSQL remains untouched. This is not independently allocated four-core calibration.
- Hard stop: available host RAM below 2 GiB; free disk below 30 GiB main / 20 GiB database; any OOM or production container health regression; total host CPU above 85% for five consecutive seconds; I/O wait above 10% for five consecutive seconds. The observed brief CPU spike alone is not a sustained-limit violation.
- An independent watchdog outside the test resource group must stop recorded test container IDs on either host if safety limits fail, its peer becomes unreachable, or public API readiness / website checks regress. Establish five-minute local baseline first. Two consecutive failed 2-second health probes stop load; latency above max(2 seconds, three times baseline p95) for three probes also stops. These safety gates add protection; they do not relax qualification thresholds.
- Monitor API readiness plus samnadoilandgas.sportbanter.online, unlimitedenergysystems.com and atlxpres.com. The retained external baseline returned HTTP 200 for all four; local baseline and ongoing checks are still required.

## Isolation, access and cleanup

Use synthetic data only, database capacity_lab with disposable-compose-only marker, unique credentials, independent Redis and mocked transports. Guard destination identity before migrations/import; never connect a seeder to across_db or the production PostgreSQL container. No real notifications/payments. Build images on free CI, not live hosts; monitor image loading as Docker daemon work can escape client resource limits.

Keep test ports loopback-only. Prepare restricted temporary SSH forwarding for CI to test gateway and main to separate test database; validate pgx socket addressing and tunnel overhead at low rate. No production proxy, DNS, firewall, VPN or Redis changes. Exact restricted key entries, resource IDs, manifests and watchdog implementation remain preparation gates before setup; no general root credential goes to CI.

Cleanup must stop only nonce-labelled, recorded test container IDs, remove only their recorded volumes/networks and exact temporary key fingerprints/tunnels. Never Docker prune, production compose down, database restore or production restart. Preserve workload journals and metrics before removing disposable data. Watchdog failure must fail closed.

## Execution decision

Assessment supports preparing a bounded shared-host pilot. It does NOT authorize setup or prove 500-session capacity. Finish executable manifest/access/watchdog validation, then obtain approval before changing either host. After low-rate identity and safety checks, use a separate coexistence ramp, followed only if safe by the UNCHANGED synchronized 500-session calibration. Full sustained qualification remains gated on calibration, resource headroom and correctness. A safety abort is neither a pass nor automatically an application defect. No 1,000-session stage, K3s installation or purchase.

## Evidence hashes

- main.json SHA256: 97be495a05bd7cef3ebd43c34944b807ebfb5983d53cdbafbf58df5452746ffc
- database.json SHA256: 984f27cd46a04564be54d45e747a58ce1734396147c3f9f701a425e77140a645
