# Single-host replacement — preparation only

This branch removes the cross-VPS PostgreSQL tunnel, Unix socket/socat services,
database relay, peer heartbeat coordination and all main-VPS test resources.
The immutable application runtime and existing k6 workload remain unchanged.

`render.cjs` derives the existing reviewed services and replaces the PostgreSQL
relay with actual synthetic PostgreSQL on the same internal Compose network.
Only the gateway publishes a port, on127.0.0.1:58080. No production credentials,
network or data mounts are used. All services are bounded by a3CPU/5.5GiB parent
slice; API/Redis/gateway/worker/tools share2CPU/2.5GiB and PostgreSQL1CPU/3GiB.
These ceilings leave nominal1vCPU/~2.3GiB outside the test on the4vCPU/~7.8GiB
database host, but do not guarantee dedicated CPU or production isolation from
shared storage, network and hypervisor contention.

`rehearse.py` is explicitly disposable-GitHub-only. It renders actual Compose,
checks effective cgroups, seeds500pairs including migration108, starts both APIs,
exports synthetic sessions, exercises the unchanged20-session/45-second first
stage, reconciles acknowledged writes, tests owned-container abort against an
unrelated sentinel, and performs exact-owned cleanup. Its result is preparation
evidence, never500-session capacity qualification. Raw synthetic credentials stay
on the disposable runner and are excluded from artifacts.

No live-server execution entry point is supplied until the independent local
watchdog/reaper, restricted generator access, interrupt recovery and full staged
execution are completed and rehearsed. Do not run the old cohost operator.
The proposed placement needs explicit review because it adds API test workload
to the production database host; no production deployment is authorized.
