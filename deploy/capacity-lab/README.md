# Disposable capacity lab

Run the `Isolated capacity lab` GitHub Actions workflow on a reviewed branch.
It creates its own PostgreSQL, Redis, two real API processes, HAProxy and a load
runner on an internal Docker network. No ports, host database volumes, production
networks, credentials or database dumps are used. API migrations run once in the
seed service. Network egress is unavailable at runtime; worker/email/push/payment
delivery is deliberately not started. The image build can download dependencies.

The runner accepts only `seed` or `run`, requires the lab marker, uses fixed
Compose service addresses and checks a synthetic database identity. It does not
accept a target URL or production environment file. Its HTTP transport ignores
proxy environment variables and refuses redirects. Do not expose these lab
services publicly or reuse their deliberately disposable credentials.

Seed: 100 buyer/provider pairs with approved paid provider fixtures, 100 service
listings/products/conversations, 2,000 Town Hall rooms, 20,000 bargaining messages,
10,000 marketplace messages and 2,000 Support messages. The workload exercises
catalogue/service browsing, authenticated buyer/provider history, both directions
of marketplace sends, Support history/replies, request creation and provider
offers/bargaining. Notification database triggers execute normally. Distinct
synthetic client IPs exercise the current Redis rate limiter rather than sending
all clients through one shared-IP allowance. This is not a fix for production
proxy-header trust or a shared-IP/abuse qualification.

`smoke` uses 20 seconds per stage, `baseline` 90 seconds, at 20/50/100 active
buyer/provider pairs. Each pair sends sequential requests then waits two seconds;
this is a deliberately busy closed-loop workload, not 100 simultaneous requests
or a production user-count claim. The first stage is cold-ish; preflight timings
are recorded separately. There is no automatic write replay. Unexpected failures
or overload stop further stages. Confirmed writes are reconciled against rows
committed in the disposable database, including after a failed stage. Both API
replicas must appear in the response headers.

Download the workflow's `isolated-capacity-<run-id>` artifact for per-operation
successful p95, errors, busy responses, throughput, replica distribution, write
reconciliation, synthetic service logs and runner hardware information. Read
successful latency alongside errors: fast failures are never counted as success.
The workflow exit status enforces correctness and absence of overload, not a
500ms latency certification. Interpret latency against the launch runbook's
initial 500ms operating target and investigate each operation separately.

The lab puts all services and generator on one shared CI machine, with per-service
CPU/memory ceilings. Its results are a repeatable software baseline. They omit
mobile rendering, real internet/TLS/CDN, media transfers, worker queue drain,
large catalogue history, hot-room contention and production hardware/network
characteristics. End-of-run Docker stats are a snapshot, not a peak utilization
measurement. Further qualification needs time-series CPU/memory/DB wait/queue-age
observations and staging on hardware representative of the main/database split.
Do not run this workload on a production host or against production databases.

No AWS or other paid resources are provisioned. The workflow consumes GitHub
Actions minutes under the repository's existing account plan and quotas. Cleanup
removes only this lab's Compose project and anonymous disposable database data.
