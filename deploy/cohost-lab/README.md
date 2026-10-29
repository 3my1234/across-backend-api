# Existing-host cohost trial preparation

PREPARATION ONLY. No live host has been changed. Do not run the old CI calibration.sh against a live Docker context: its fixed service names and destructive disposable reset/cleanup assume a single throwaway lab.

## Completed artifacts

- render.cjs renders independent main/database Compose JSON offline from the retained lab service definitions. Requires a unique nonce and digest-locked images. API pools remain 8 and admission 64. Generator is excluded from main. The main internal network's postgres alias is a socket-to-TCP bridge to the NEW test PostgreSQL on the other server; existing hardcoded lab destination remains unchanged.
- safety.py evaluates strict resource/health thresholds and, only with explicit --stop-test, inspects exact full container IDs plus unique ownership labels before stopping. It rejects a mixed/unowned inventory before any stop. No name-prefix deletion or prune.
- watchdog.py reads Linux CPU, memory, disk and OOM counters, protected production-container states, public health and a peer lease. Dry-run default. Any collection error expires its lease and triggers owned-only stop when armed. Both hosts need independent watchdogs and restricted reciprocal lease refresh. An external runner must stop generating if either lease/status fails.
- Manual free-CI workflow cohost-preparation.yml tests offline behavior and actually stops a disposable labelled container while refusing to stop an unrelated CI sentinel. This is tool validation, not a load or capacity test.

## Proposed host budget (approval required before installation)

Main test parent slice: CPUQuota=200%, MemoryMax=2684354560, MemorySwapMax=0, CPUWeight=10, IOWeight=10, TasksMax=2048. Database test parent slice: initially CPUQuota=100%, maximum approved trial 300%, MemoryMax=3221225472, MemorySwapMax=0, CPUWeight=10, IOWeight=10, TasksMax=512. These must be NEW slices; never edit an existing service or production slice. Main per-service ceilings compete under its aggregate cap. Database render starts postgres at 1 CPU; any increase is test-only and must be recorded. Do not allocate all four cores or advertise four independently reserved DB cores.

Before starting: verify systemd cgroup driver, v2 controllers and each container's actual cgroup ancestry/effective quotas. Abort if any requested restriction is unsupported or bypassed. Keep host watchdogs outside the test slices. Check existing network/subnet and loopback port collisions read-only; do not move production networks. Exact digest archives must be built on free CI. Docker image loading is daemon work and needs monitoring independently of the invoking shell.

## Data and transport boundaries

Database process/volume/credentials are disposable lab only. No production env file or mount, production database, production Redis or actual transport credentials. Existing synthetic credentials are fixed by the lab binary and must stay confined to the isolated network/tunnel; they are not production secrets. Do not expose the test PostgreSQL or gateway publicly. All images require recorded digest approval. Seeding must verify new-container ownership and capacity_lab identity; no use of production across_db or its data directory.

Restricted key plan for review: CI key permits only forwarding to main 127.0.0.1:58080; main-to-DB key permits only DB 127.0.0.1:55432. Disable PTY, agent forwarding, X11 and unrestricted commands; synthetic fixture retrieval/peer lease exchange require separate narrow forced-command wrappers under the nonce directory. No unrestricted root key or password is copied to CI. The main SSH Unix socket forward is mounted only into the labelled postgres bridge. Exact authorized_keys lines, verified image digests, lease helper and orchestrator are NOT installed/preapproved by this document.

## Safety and staged execution

Five-minute local successful baseline for all four URLs is required before watchdog arming. baseline.json maps each fixed URL to measured p95 seconds. Manifest includes run, role, exact full test container_ids and production_container_ids. A protected external lease refresher updates peer.lease only after checking peer local.lease freshness; touching blindly is forbidden. Hosts log timestamped safety samples; free runner records safety telemetry and cancels traffic on lost heartbeat. Validate forced collector failure and peer loss in disposable CI before live arming. Current unit checks do not establish these end-to-end guarantees.

Read assessment report for thresholds. Hard stops: memory reserve below 2 GiB, disk reserve below 30/20 GiB main/DB, OOM, production health failure, CPU >85% for five seconds, IO wait >10% for five seconds, public health/latency regression or stale peer heartbeat. Main's observed short 88% CPU spike is a reason for these controls. Safety abort is not qualification success or proof of an application design defect.

Approved execution must progress through low-rate destination/transport verification and a separate coexistence ramp. Then retain the original synchronized 500-session workload, fixture dimensions (large-busy, 500 actor pairs), policy hashes, latency/error and complete reconciliation/receipt requirements. Do not substitute ramp success for the unchanged burst. No 1,000 stage. No production change, DNS, firewall, VPN, K3s or purchase.

## Outstanding release gates

This package is not yet a runnable cross-host qualification orchestrator. Still required: digest resolution/export and hash verification; cgroup enforcement proof; separate-host fixture migration/identity/backup-reset adapter replacing old destructive dc helpers; restricted key/lease helpers; free-runner tunnel and unchanged-suite adapter; mocked-transport/worker isolation and actual cross-host timing verification; end-to-end watchdog/cleanup drills. These are concrete integration work, not reasons to repeat the already-completed SQL investigations. Obtain user approval of exact live-host setup after these checks and before any installation/load.

Cleanup stops only recorded owned IDs, then separately verifies exact nonce-labelled volumes/networks before removal. Remove exact temporary key fingerprints/socket helpers and runtime test slice units only. Keep evidence before disposable deletion. No production restore/restart or broad Docker prune.
