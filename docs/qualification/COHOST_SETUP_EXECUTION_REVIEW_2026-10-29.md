# Two-existing-VPS execution review ? setup-only gate

29 October 2026. Run nonce `cohost-6d6c5f9220874022`. **No purchase. No production change. No application load authorization.**

The disposable safety/transport checks have passed. Actual VPS endpoint verification still requires creation of separate idle test resources and temporary restricted SSH keys. This document requests approval for that SETUP ONLY; it does not claim the full 500-session executor is qualified or ask to start it.

## Evidence completed

- Read-only actual hosts: Docker systemd driver/cgroup v2 with CPU, memory, IO and PID controllers; proposed loopback ports unused; main test subnet does not overlap current Docker subnets; SSH permits required forwarding. Earlier passive samples: main CPU p95 26.32%, maximum 88.46%, RAM available6.28GiB; DB CPU p955.63%, maximum8.85%, RAM available7.07GiB. Each host has4vCPU. Samples are not peak guarantees or bandwidth qualification.
- Final disposable CI40933059552 at0e2739a403350532c1a4bb9679894c37df60a949 PASSED17policy tests, six watchdog-failure/real-stop drills, real volume/network cleanup plus refusal of unrelated data, and real PostgreSQL traffic through the full internal Docker `postgres:5432` alias ? Unix socket ? restricted SSH path. Shell/non-test forwarding denied. No live host was involved.
- Image bundle CI40930181592 at13d316c62ce8bdd35c1c2fe72bf3d4d357581914 PASSED and built immutable binaries. Lab-only CPU metadata now accepts the bounded3CPU profile; targeted Go tests passed and4/200remain rejected. Production API/migration/client code unchanged.
- Image archive SHA256: `020bacefeffd12bdfa460c165b43b471f9f987b5da7579bdfd219137ceb2c366`. Corrected image manifest AND full208MBarchive downloaded and independently SHA256-verified locally; LOCAL_VERIFIED.txt records completion. Execution must refuse missing or mismatched archive. Earlier image bundle40925392518 is superseded for fixture execution because its lab binary rejects3CPU metadata.

## Allocations

| Resource | Setup-only ceiling | Later proposed 500 ceiling, NOT yet authorized |
| --- | --- | --- |
| Main NEW test aggregate |2CPU,2.5GiB,swap0,Tasks2048,CPU/IO weight10|Same aggregate; API0.75CPU each,pool8/admission64unchanged; gateway1CPU,worker1CPU compete under aggregate|
| DB NEW test PostgreSQL |1CPU,3GiB,swap0,Tasks512,CPU/IO weight10|Maximum3CPU/3GiB after review; productionPG unchanged|
| Free CI generator |Not started|Existing qualified2CPU/4GiB; original raw recorder/client/profile|

No cores are claimed independently dedicated on these shared VPS hosts. Docker daemon image import, SSH server-side forwarding and kernel work are not all constrained by the test parent slice; host safety monitoring is required. Four allocated VPS CPUs do not guarantee exclusive physical cores. No production container limits or pools are edited.

## Automatic stop conditions

Before setup, collect a fresh successful five-minute baseline for API readiness, both unrelated websites and the landing site. Setup guard runs outside the test slice and expires after15minutes, stopping recorded owned test containers. Full-load guard must later include independent peer/CI lease failure and generator termination before any load approval.

Stop on available RAM below2GiB; free disk below30GiB(main)/20GiB(DB); OOM; any protected container health regression; CPU>85%for5seconds; IO wait>10%for5seconds; two consecutive public-health failures; three latency regressions above max(2seconds,3?baseline p95). Missing telemetry or stale peer also stops full testing. A safety abort is not a passing capacity result. First observed brief88%CPU spike is not sufficient evidence of sustained headroom.

## Setup-only actions requested

Stage/hash-check exact reviewed archives and files under `/root/atlxpres-cohost/cohost-6d6c5f9220874022` on each host. Create NEW capped runtime slices, nonce-labelled internal networks, disposable PG volume and idle containers. Start only new test PostgreSQL on DB and new test Redis on main initially. No seed, API, worker, payment, email or push activity. Public application routes/Coolify/productionPG/Redis/DNS/VPN stay untouched.

Add three-hour-expiring public keys with restrict/noPTY/noagent/noX11, forced reviewed helper and exact permitopen targets: CI?main127.0.0.1:58080; main?DB127.0.0.1:55432; CI?DBstatus/control helper has no forwarding. Pin real host keys obtained through existing trusted operator SSH. No root password or unrestricted root key goes toCI. These authorized_keys additions and new test objects require approval; they are not yet installed.

Verify the main?DB test tunnel with the owned db-probe container and `SELECT current_database(),current_setting('cluster_name')`; cluster_name must match the nonce. Verify effective cgroup ancestry/limits, not just requested quotas. CI authentication/forwarding checks use restricted keys and at most low-rate read-only probes. Application endpoint/gateway readiness must still be verified after separately authorized synthetic-fixture preparation.

The SSH ingress changes the test-only trusted-edge address: add ONLY172.29.248.1/32(the new loopback-published Docker gateway edge) to the existing synthetic trusted chain. Do not alter rate600, pool8, admission64, password/security processing or production trust lists. Before application measurement, prove the actual ingress address and preservation of distinct synthetic identities. Existing spoofing/repeated-header Go regressions pass. Abort if the real ingress differs; do not blindly expand trusted CIDRs.

## Exact setup commands after protected file staging and explicit approval

These are reviewed commands, not instructions to execute now. `approval.json` is deliberately absent; only a false-valued template exists. Actual authorization must be recorded after user approval. Uploads for BOTH hosts must finish before starting guards, so slow transfer cannot consume their15minute window.

On each host, with its own rendered role-specific compose/request and unchanged nonce:

```sh
python3 /root/atlxpres-cohost/cohost-6d6c5f9220874022/tools/baseline.py /root/atlxpres-cohost/cohost-6d6c5f9220874022/baseline.json
python3 /root/atlxpres-cohost/cohost-6d6c5f9220874022/tools/stage_host.py /root/atlxpres-cohost/cohost-6d6c5f9220874022 --execute
python3 /root/atlxpres-cohost/cohost-6d6c5f9220874022/tools/install_keys.py /root/atlxpres-cohost/cohost-6d6c5f9220874022 --execute
```

Main-to-DB forwarding uses a NEW bounded unit under `cohost6d6c5f9220874022main.slice`, a test-only private key, pinned host key, `-N`, `ExitOnForwardFailure=yes`, and `-L /root/atlxpres-cohost/cohost-6d6c5f9220874022/tunnel/.s.PGSQL.55432:127.0.0.1:55432`. No new DB public port. Exact pinned host-key file cannot be populated before the operator-authenticated check; no StrictHostKeyChecking bypass is permitted.

After that new tunnel is established, on main:

```sh
python3 /root/atlxpres-cohost/cohost-6d6c5f9220874022/tools/verify_transport.py /root/atlxpres-cohost/cohost-6d6c5f9220874022/manifest.json
```

Expected setup duration approximately15?25minutes plus artifact transfer. No scheduled load starts when setup succeeds. If image import, runtime cgroups, actual tunnel or public health checks fail, stop and retain evidence.

## Rollback/cleanup

Each host uses its own protected immutable-ID manifest. Preserve evidence first, then:

```sh
python3 /root/atlxpres-cohost/cohost-6d6c5f9220874022/tools/cleanup.py /root/atlxpres-cohost/cohost-6d6c5f9220874022/manifest.json --execute --remove-disposable-data
python3 /root/atlxpres-cohost/cohost-6d6c5f9220874022/tools/install_keys.py /root/atlxpres-cohost/cohost-6d6c5f9220874022 --execute --remove
```

Only recorded matching-labelled containers/unused volumes/empty networks may be removed. Stop only NEW nonce-named anchor/guard/tunnel units after verifying their test-slice ownership. Keep production services running. No broad Docker prune, global image removal, production restore/restart, or unrelated key deletion. Cleanup/expiry cannot undo an image-import CPU/I/O transient; that shared-host limitation remains explicit.

## Full 500 sequence ? still blocked, no approval requested yet

1. Pass actual runtime transport/resource guards and mock/no-egress checks. Validate the split-host fixture/snapshot/reset/journal adapter with a small disposable correctness smoke before capacity measurement. This adapter remains a concrete implementation/validation gate.
2. Under separately recorded load authorization, generate the SAME large-busy/500actor-pair synthetic fixture, snapshot it, then use isolated reset between stages. No production data copy.
3. Separate45second small smoke and180second gradual-arrival coexistence stages; do not interpret gradual success as burst qualification. Preserve exact client/model hashes and unique identity accounting.
4. Restore baseline and run original synchronized500-session180second calibration with actual workers/mocked transports. Do not replace it with the weaker arrival-rate `burst` profile(which tolerates controlled overload). Stop on any unchanged gate failure.
5. Only after calibration/resource/correctness pass: restore baseline, run unchanged500established-session30minute sustained stage,60second recovery, acknowledged-write/authorization/order/idempotency/presence reconciliation, send-queue drain and genuine15minute receipt eligibility/remaining tail(max20minute observation). No timestamp aging or shortened receipt wait. No1000stage.

Original gates remain: zero controlled-overload responses; error rate<0.005; content correctness1; p95<500ms/p99<1500ms globally and per operation; polling p95<17s/p99<20s; target sessions actually achieved; no dropped iterations; all acknowledged writes stored once, zero duplicate keys, exact expanded driver/TownHall checks and queue/receipt proof. Resource/generator headroom is mandatory. Approximate measurement/recovery/drain portion60?75minutes excluding fixture preparation/transfers; no capacity or launch success promised.

**Decision requested now: allow ONLY capped isolated setup and read-only endpoint/cgroup verification. Keep fixture preparation and the500sessionload blocked.**


## Execution amendment: retired Samnad site (user confirmed29October2026)

User confirmed samnadoilandgas.sportbanter.online was deliberately removed. It is excluded from current public health checks. Protect APIready, unlimitedenergysystems.com, atlxpres.com and every currently running production container with unchanged thresholds. This is an inventory correction, not authorization to restart or modify any production website.

Both imagearchives were uploaded successfully before baseline stopped; retain them. Use qualification/Resume-CohostSetup.ps1, which verifies both existing remote archiveSHA256 values and uploads ONLY small updated packages. Same reviewednonce/immutableCompose/resourcecaps; fresh protected-site baselines and actualruntimeverification still mandatory. Refresh only uninstalled temporary restricted key expiry to invocation+3hours within existing setup-only authorization. Original Invoke-CohostSetup.ps1 is not a resume path. No fixture/load authorization.
