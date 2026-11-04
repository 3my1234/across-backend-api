# Single-host replacement — preparation only

This branch removes the cross-VPS PostgreSQL tunnel, Unix socket/socat services,
database relay, peer heartbeat coordination and all main-VPS test resources.
The immutable application runtime and existing k6 workload remain unchanged.

`render.cjs` derives the existing reviewed services and replaces the PostgreSQL
relay with actual synthetic PostgreSQL on the same internal Compose network.
No container publishes a host port. Restricted SSH can forward only to the
gateway's private address172.29.248.10:8080. No production credentials,
network or data mounts are used. All services are bounded by a3CPU/5.5GiB parent
slice; API/Redis/gateway/worker/tools share2CPU/2.5GiB and PostgreSQL1CPU/3GiB.
These ceilings leave nominal1vCPU/~2.3GiB outside the test on the4vCPU/~7.8GiB
database host, but do not guarantee dedicated CPU or production isolation from
shared storage, network and hypervisor contention.

`rehearse.py` is explicitly disposable-GitHub-only. The CI foundation mode renders
actual Compose, checks effective cgroups, seeds10pairs including migration108, starts both APIs,
exports synthetic sessions, exercises the unchanged20-session/45-second first
stage, reconciles acknowledged writes, tests owned-container abort against an
unrelated sentinel, and performs exact-owned cleanup. Its result is preparation
evidence, never500-session capacity qualification. The unchanged500-pair seed
component already completed in437.359seconds on1CPU/3GiB; its checksum-protected
result is retained and checked without downloading historical artifacts. Its
containing historical workflow failed a later queue preflight and is NOT called
a passed workflow. The live job separately enforces the original500-pair
large-busy fixture; reduced foundation fixtures are rejected there.
Raw synthetic credentials stay
on the disposable runner and are excluded from artifacts.

The supervisor, restricted access handler, source/approval gates and operator
are drafts, not a reviewed executable release. Do not run the old cohost operator
or a draft live command. Actual SSH/control/HTTP access, local guard faults and
systemd ExecStopPost cleanup must pass disposable replay before review.
The proposed placement needs explicit review because it adds API test workload
to the production database host; no production deployment is authorized.

The test-only recipient verifier now streams every expected message in shards
of at most10,000, rather than rejecting an entire run above10,000 messages. It
still checks each intended recipient and exact body, retains the1,000-page bound
per shard and12-minute context, and fails above1,000,000 total rows. No sampling
or application latency/error/integrity threshold changes. Focused tests cover
10,001 messages, wrong/missing bodies, HTTP failure and distinct recipient identities.
Only this verifier and its tests may differ under cmd/capacity-lab. A separately
compiled, checksum-pinned verifier is mounted read-only into the verifier alone;
cached API/seed/worker images remain unchanged. This candidate still needs the
full disposable rehearsal before live approval. Queue recovery uses an exact
labelled pre-load probe, removed before workload so fake tickets cannot contaminate
the actual worker/provider-journal checks.
