# Existing two-VPS qualification: assessment first

29 October 2026. **No additional server purchase or paid test resource is authorized.** The Hetzner proposal and its pending approval request are withdrawn by the user's latest instruction. Retain the old proposal only as history.

## Current action

Read-only, passive assessment of both existing Contabo hosts. Automated SSH authentication was attempted with BatchMode and failed on both; no host command executed. The operator can run one local PowerShell wrapper, `qualification/Collect-ExistingVpsHeadroom.ps1`, entering passwords locally. It observes each server for about60seconds and saves JSON reports in `qualification/existing-vps-assessment-20261029/<UTC timestamp>/`; `existing-vps-assessment-latest.txt` identifies the capture. No need to paste reports because Codex can read the saved files.

Collector reads per-core CPU busy/steal/I/O-wait counters, RAM/swap, load, disk space and passive disk/network counter deltas, container CPU/RAM limits, running/health state and Docker stats. It collects public routing domain names from router rules to identify the unrelated website's health-monitoring target. It does not retrieve Config.Env, issue SQL, decode backups, run fio/iperf/stress, restart services or change settings. Other container names are anonymized. CPU-delta arithmetic, metadata redaction and PowerShell syntax checks passed. These are tool checks, not live resource results.

## Proposed topology, subject to the actual assessment

- Free GitHub Actions runner: the already-qualified single500-session generator, twoCPU/4GiB. It has measured headroom; splitting it across runners is not currently necessary and would require preserving globalidentity, timing and journal accounting.
- Main VPS: a separately named test API/gateway/worker/Redis/mock stack only if measured spare CPU/RAM/disk permits. Coolify, production ATL Xpress, existing Redis/proxy/VPN and the unrelated website remain running with unchanged configuration.
- Database VPS: a NEW PostgreSQL container, independent process/credentials/volume/database and loopback-only test port, only if coexistence headroom permits. Never place synthetic tables in `across_db`, use production credentials, or reuse the production PostgreSQL container/data directory.
- No production data copy is required: retain the existing synthetic fixture and original workload/client hashes. No real email, push, billing or payment delivery.

Exact test CPU/memory/I/O/connection ceilings must follow the measured headroom, not consume every visibleCPU by default. A fourCPUdatabase quota cannot be claimed independently available while production shares the same four CPUs. A test at smaller safe limits is valid only for the measured shared-host configuration; do not label it dedicatedfourCPU or production capacity.

Logical data and routing isolation can prevent writes to production, but co-hosting inherently shares CPU, kernel, memory bandwidth and storage. Do not promise zero performance impact. Reserve production headroom, use hard test limits and continuous customer/unrelated-site health monitoring with a separate stop controller. Stop on production readiness/website regression, excessive hostCPU/steal/I/O-wait, falling memory/disk reserve or other reviewed safety criteria. A safety-aborted run is not a qualification pass.

## Connectivity to evaluate without changing existing routing

Prefer authenticated SSH forwarding over existing SSH reachability: freeCI generator to a loopback-only main-host test gateway, and a main-host Unix-socket forward to a loopback-only separate test PostgreSQL instance on the DBhost. This can avoid publictestDBports, DNS, firewall, production proxy or VPN changes. If keys are needed, prepare short-lived narrowly restricted keys (only the exact test forward and synthetic fixture retrieval), show the precise authorized_keys additions/removal plan and obtain approval before changing either host. Do not distribute production SSH credentials toCI. Verify pgx Unix-socket addressing, permissions, effective DB destination and tunnel overhead in a low-rate transport check before any capacity test.

Alternative placements, or additional freeCI workers for isolated components, remain options if the assessment shows insufficient safe headroom. Network reachability/latency and every worker's identity/journal partition must be proven before use. Do not simply repeat known inconclusive500bursts.

## Gated execution after assessment and explicit plan approval

1. Present actual host metrics, proposed caps, independent test volume/container/network ownership, monitoring targets, connectivity changes and exact cleanup commands. Obtain approval before creating test resources or executing potentially disruptive traffic on these livehosts.
2. Check current usable backup and release rollback references without restoring or modifying production. Test cleanup must target only recorded test resource IDs/volumes/keys/tunnels, never broad Docker prune or production rollback.
3. Start only the approved isolated containers and low-rate connectivity/functional checks, while monitoring production and the unrelated site. Confirm test database identity markers and test-only routing before any seeding/migration command.
4. Use a separate realistic ramp to observe coexistence, then the unchanged controlled500-session burst/qualification profile. Do not ease its existing latency, HTTPerror, authorization, bidding, idempotency, presence, write-reconciliation or recovery gates to obtain a pass. Ramp results do not substitute for the original burst gate.
5. Only after the calibration and resource/safety evidence passes, run the sustained500stage with full reconciliation, actualworkers/mockedtransports, queue drain and realreceipt-tail checks. No1000stage. No throughput claim for concurrentmediauploads or other untestedjourneys.
6. Stop/remove only test-owned resources if a safety gate fails. Production must not be restarted, redeployed, migrated, or reconfigured as a recovery shortcut. Preserve evidence and update the handover.

## Pending evidence

Fresh passive reports from both hosts are required before declaring coexistence feasible or selecting allocations. A one-minute low-load sample is not a peak-load guarantee; combine it with available historical monitoring and keep watchdogs active during any approved test. The effective production PostgreSQL container quota check remains part of the new inventory. No new paid resources, K3s installation or production-changing load test has been performed.
