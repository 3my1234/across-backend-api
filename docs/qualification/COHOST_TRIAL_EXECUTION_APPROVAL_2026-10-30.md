# Existing-server 500-session trial ? execution approval review

30 October 2026 (Africa/Lagos). APPROVAL REQUIRED. No live load has been authorized or dispatched.

## Scope and preserved evidence
Completed setup proof: cohost-0cfe085cbad1ec57. Both five-minute protected-site baselines passed, exact main-to-isolated-DB identity passed, both cgroups verified and both cleanup markers reviewed. 292 captured setup watchdog samples were healthy, with no OOM. This is setup evidence, not capacity evidence. Do not rerun that closed nonce.

The executable trial uses a fresh nonce, the same verified 199 MiB image archive already on both hosts, unchanged application/lab binaries from 13d316c62ce8bdd35c1c2fe72bf3d4d357581914, and the qualified original recorder plus reversible local prewarm. It checks the successful generator run40866958782 and exact executed client SHA256 dad72e9b2b8dd7dd9fca94bb6a0d6856d4081a49d301b53e3e133c127d01ea49. No images need uploading again. Recreating disposable resources is execution, not reopening the setup investigation.

## Explicit resource decision
- Main test aggregate: maximum2CPU/2.5GiB, swap0, CPU/IO weight10, Tasks2048. APIs retain pool8/admission64; gateway1CPU and worker1CPU compete under this aggregate. Production and unrelated website limits remain unchanged.
- Database test aggregate: maximum3CPU/3GiB, swap0, CPU/IO weight10, Tasks512. This INCREASE from setup-only1CPU requires this separate approval. Production PostgreSQL is unchanged.
- Generator: existing free standard GitHub Actions runner; k6 capped2CPU/4GiB/swap4GiB(no additional swap), Tasks512. SSH/coordinator and telemetry also consume the runner, so actual generator headroom is gated using the original limits: CPU p95<70% of quota, initial-burst p95<85%, throttled periods<1%, peak memory<70%, no OOM, sufficient 100ms sample coverage and offered/completed accounting. Capacity claims remain conditional on full evidence review.
- No dedicated physical CPU guarantee. This3CPU/3GiB shared-host profile is not equivalent to production4vCPU/~8GiB and cannot establish production capacity by itself.
- No purchase, larger paid runner, DNS/firewall/Coolify changes, production migration or K3s installation.

## Fixed stages
| Stage | Concurrent session target | Workload duration | Workers |
|---|---:|---:|---|
| Small baseline (smoke) |20|45seconds|Off; durable queue records retained|
| Established-session baseline |100|180seconds|Off; durable queue records retained|
| Preliminary ramp |0to500 over30seconds, then500|180seconds total|Off; not a burst/capacity pass|
| Original simultaneous sustained stage |500 established sessions start together|1,800seconds|Actual workers with isolated mocked transports|

Each stage authenticates its own target gradually before measurement, using the original established-session client. Every actor remains distinct. Same marketplace/chat/Town Hall bidding/driver-courier position updates/nearby searches and15-second chat polling. New synthetic run UUID per stage isolates acknowledgement journals; fixture population/data remain synthetic and grow through the stages, rather than claiming matched clean-state benchmarks. Earlier queued jobs remain for the final worker stage to drain; measured initial backlog is reported.

Workload duration alone is36m45s. Authentication, synthetic data preparation, recovery, five-minute send-drain and up to20-minute real receipt tail add time. Expected elapsed approximately75?110minutes after execution begins, plus baseline/password entry; hard CI/host lifetime120minutes. Exhaustion is failure, not an automatic extension. Existing worker/mock65-minute lifetime is unchanged; if it expires before receipt gates pass, the trial fails. This is a known bounded-runtime constraint, not permission to shorten receipt eligibility.

## Unchanged gates
Original k6/session model and threshold definitions remain unchanged: per-operation p95<500ms,p99<1500ms, unexpected errors<0.5%, controlled overload0, valid content100%, full intended session starts/VUs, no dropped arrivals, chat delivery p95<17s/p99<20s, expanded-driver and bid checks, both API replicas. Final qualification requires the original Go acknowledgement/offer/bid/recipient/presence reconciliation and original advancement gate, plus send-drain, provider acknowledgement and actual receipt eligibility. A gradual-ramp pass is never counted as a simultaneous-capacity pass. The independent generator prerequisite is not a substitute for observing actual generator headroom throughout this trial.

## Runtime safety and cleanup
Fresh five-minute/60-sample/site baseline with HTTP transfer<=2seconds and complete observer call<=2.5seconds (explicitly approved split timing policy) for APIready, Unlimited Energy and ATL landing; freshness15minutes. Original limits remain.
Watchdogs stop owned test resources on production unhealthy/OOM, available RAM<2GiB, disk<30GiB(main)/20GiB(DB), CPU>85%for5seconds, IOwait>10%for5seconds, two health failures, three latency failures(max2seconds or3xbaseline), or peer lease age>5seconds. CI accepts only both host identities with age<=3seconds and advancing sequences before refreshing either peer; duplicate fresh reads are not new proof and never refresh leases.
Separate outside-slice reapers clean on dead/stale watcher or120minute lifetime. CI failure/cancellation stops its owned generator and requests exact-ID cleanup on both hosts. Operator wrapper independently repeats idempotent exact-nonce cleanup and retrieves allowlisted evidence; no global Docker prune. Cleanup failure prevents a full-pass marker and is reported separately. Setup-only approval cannot start API, worker or provider, seed fixtures, or upload trial evidence.

## Credentials, ingress, evidence
Temporary root-owned keys remain expiry-bound and forced-command/permitopen restricted. No public database port. Gateway is loopback58080, reached by CI SSH forwarding to localhost8080; the original gateway hostname and single reviewed test-only trusted edge are preserved. The operator wrapper puts ONLY these temporary restricted keys, pinned host keys and nonsecret approval metadata into one temporary GitHub repository secret COHOST_TRIAL_BUNDLE; it refuses an existing secret of that name and deletes it afterward. No production secret is uploaded. This temporary secret use is part of the requested approval.

Artifacts contain raw k6 metrics, request journals, per-stage summary and SQL profiles, pool observer, host/container/cgroup metrics, queue age/depth observations, notification mock journal/ack/receipt evidence, recovery, proof hashes and cleanup results. Bearer fixtures, passwords, mock private key and SSH keys are excluded. Acknowledgement reconciliation is attempted after performance abort; unavailable DB/evidence is an explicit failure, never interpreted as zero writes.

## Exact operator sequence after review
Offline preparation is safe and does not authorize execution:
`python .worktrees/launch-qualification/deploy/cohost-lab/prepare_trial_execution.py C:/Users/EMMA/Desktop/gouwu --safety-run <passed-current-source-run>`
It generates a fresh review with FALSE approval flags and qualification/Invoke-CohostQualification.ps1. The final prepared command, ONLY AFTER USER APPROVAL, is:
`& "C:\Users\EMMA\Desktop\gouwu\qualification\Invoke-CohostQualification.ps1" -ApproveTrial`
That command records the approved exact source and envelope, stages small tools using existing images, verifies fresh guards/connectivity, dispatches registered cohost-preparation.yml with execute=true and exact nonce, watches it, downloads evidence, removes temporary repository secret and cleans test resources. Workflow default/push is offline-only; its live job requires explicit dispatch plus valid nonce-bound host/CI approvals and an exact-source successful disposable safety run. No1,000-session path exists.

## Remaining release scope
No real email/push delivery, payment, media transfer, store submission or native rendering is exercised by this trial. Production backup restore rehearsal, received alerts/rollback and critical device/payment readiness remain separate release evidence requirements. A test pass qualifies this workload/profile, not millions of users or automatic broad launch.

## Final validated package

Source: dbffabfddb183497a1c796b2871bba3ef11a7226. Disposable CI41014532150 PASSED all63Python regressions, Node renderer/session-stage gate mutations, real owned-container shutdown/cleanup and restricted internal PostgreSQL transport tests. Live qualification job SKIPPED. Local generated PowerShell AST passed.

Current fresh trial nonce: cohost-0c667adb06298ad8. All setup/load/fixture approval flags remainFALSE. Operator SHA256: 78fefc462c9da69fbd9c6856996b6b134a71a42308b615473d8ea193ac81d55e. Public validation record: qualification/COHOST_TRIAL_FINAL_VALIDATION.json. CIlogs/metadata retained qualification/cohost-integration-20261030/41014532150. Source and safety proof are frozen; do not push a new branch HEAD between approval and this run.

Continuous observer transition is validated against separate root-owned approval without restarting the watchdog or creating a heartbeat gap. Generator uses original root container user for private synthetic fixture access. Intentional pool-observer shutdown is distinguished from premature loss. No application, DB, Redis, DNS, K3s or live-host mutation was performed during this preparation.
