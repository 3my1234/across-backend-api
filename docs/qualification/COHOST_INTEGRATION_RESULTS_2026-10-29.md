# Cohost integration evidence and remaining live gate

29 October 2026. Run 40924379095 PASSED at 872bbc2903570db30a4766ef7f5db0c6f132c319.

## Proven in disposable free CI

13 policy/relay/resource-file tests passed. Six real watchdog-loop drills used simulated sensors and actual Docker stop commands: low memory, sustained CPU, stale peer, production-health regression, public-health regression and collector exception. In every case the owned disposable test container stopped, unrelated sentinel stayed running and local heartbeat disappeared. Sensor simulation is intentional; this does not prove actual VPS sensors or controller limits.

A real PostgreSQL container was reached through OpenSSH Unix-socket forwarding with pinned host key, public-key authentication, restrictive authorized_keys and strict file modes. SELECT returned capacity_lab:disposable-compose-only through the tunnel. Arbitrary remote commands and forwarding to a non-test port were refused. No database public listener was created. This tests the transport mechanism, not end-to-end connectivity to the actual Contabo hosts.

Failed preparation runs retained honestly: 40923671709 accepted PostgreSQL's temporary initialization readiness too early; corrected to wait for successful TCP SELECT from capacity_lab. 40923970220 and 40924166836 failed SSH CI account/file checks; final proof uses hosted-runner PAM account validation and a protected /root temporary key directory, retaining StrictModes=yes, PasswordAuthentication=no and pinned host key. No production SSH policy changed. Final successful run contains all drills, rather than inheriting results from failed runs.

## Actual host read-only prerequisites

Both hosts report Docker systemd cgroup driver, cgroup v2 and cpu/io/memory/pids controllers. No listener observed on main127.0.0.1:58080 or DB127.0.0.1:55432. Main Docker subnets do not overlap proposed172.29.248.0/24. Current sshd effective policy permits TCP and Unix-socket forwarding and public-key authentication. Root login is enabled already; this was read, not altered. No capacity observation repeated; earlier headroom report remains a short snapshot. Low network counters show observed use only, not independently qualified bandwidth.

## Specific unresolved boundary

Actual VPS tunnel/pgx routing, image digests/archive staging, runtime aggregate cgroup ancestry, full independent peer relay failure chain and split-host fixture reset/qualification adapter are NOT YET qualified. Neither server has the test endpoints or temporary restricted keys. These live checks require approved creation of test-only resources; they cannot truthfully pass while no such endpoints exist. Do not call this a 500-session-ready package or request approval to start load yet. Complete reviewed setup commands and integration adapter before that request. No infrastructure purchase, production modification, live load or new SQL investigation.

Full logs/metadata/SHA256SUMS retained at qualification/cohost-integration-20261029/40924379095/. Fresh read-only prerequisite reports are identified by qualification/cohost-prerequisites-latest.txt. Never upload synthetic fixture session tokens or private SSH keys as evidence.
