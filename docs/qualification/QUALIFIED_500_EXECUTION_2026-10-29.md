# Gated sustained500 execution preparation ? 29 October 2026

Prepared, not dispatched. Source commits8a5796b/3e0b432/1f01ea1 on experiment/launch-qualification add only workflow/glue/provenance; internal application code, migrations, session client/policy and existing measurement/recovery/drain/receipt bodies remain unchanged from the qualified batch candidate.

The registered worker-role-qualification.yml now offers explicit phase `qualified-500`. It targets exactly500 sessions for1,800 seconds and has no1,000 dispatch path. It requires:

1. Completed successful1,800-second generator artifact, unchanged client hashes, original gates and actual requested/inspected memory allocation match.
2. Completed successful180-second actual-stack calibration at the requested8GiB database/2CPU and matched generator allocation.
3. The calibration's measured/reconciliation/recovery exits all zero, correct500/180 configuration and exact generator proof reference.
4. An explicit evidence review record at deploy/notification-lab/qualified-500-approval.json. That file is intentionally absent until calibration evidence is reviewed. It must identify exact generator/calibration runs and source, allocations, authorized500-sustained-1800s profile, and positive database/shared-host headroom review. It is an internal lab evidence gate, not production authorization.
5. Unchanged internal/migration/cmd/session code versus the approved calibration source, with14GiB available host memory for DB8GiB/gen4GiB before preparation.

The wrapper qualified-500.sh extends only Compose argument wiring for the existing notification-lab/ramp.sh, session-lab/qualify-ramp.sh and receipt-tail.sh. It does not change the measured client, user journeys, think times, admission/pool settings, latency criteria, verifier or15-minute receipt eligibility. Worker/mock delivery remains isolated. The original60-second readiness recovery, write reconciliation, send drain, provider acknowledgement and real receipt tail remain active. No real payment/notification transport.

Additional timestamped cgroup/host telemetry is outside k6 and retained. Full artifacts include raw metrics, executed client/suite hashes, fixture hash, application source, database/pool/queue observations, original/generated runner hashes and all abort/final write reconciliation. Existing worker/mock65-minute lifetime and75-minute job budget remain; check observer coverage explicitly rather than assuming it lasted.

Local YAML parsing, embedded Python parsing, Bash syntax, source-diff guard and fail-closed gate ordering checks passed. This is not a claim that the full CI execution path has run or500capacity is qualified. No dispatch until prerequisites pass. The older session-lab ramp workflow remains pinned to historical source proof and must not be invoked blindly.

Current outstanding prerequisite: explicit4GiB original-recorder generator40866958782 at5dc1fc9 is running. Earlier2GiB sustained40855200111 failed memory; compressed recorder40865534032 failed initial CPU and was rejected. No calibration has started.

Only after successful generator and independently reviewed calibration, the future command is:

    gh workflow run worker-role-qualification.yml --repo 3my1234/across-backend-api --ref experiment/launch-qualification -f phase=qualified-500 -f generator_run=<passed-long-run> -f calibration_run=<reviewed-calibration> -f calibration_memory=8g -f calibration_generator_memory=4g

The evidence record is deliberately not provided as an all-true example. Do not manufacture it from a success badge alone; review full duration/concurrency, generator/host CPU and memory, database pressure, pool/locks, errors, write integrity, queue age and recovery using the original audit gates. Preserve failed artifacts and stop escalation on any failed prerequisite. Production, K3s installation, purchases and1,000 remain unauthorized.
