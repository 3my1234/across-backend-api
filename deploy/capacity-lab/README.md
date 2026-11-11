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

All scenarios use 100 buyer/provider pairs with approved paid Gold provider fixtures (synthetic NGN5,000 receipts, 60 combined posts and 10 simultaneous Town Hall rooms),
100 marketplace conversations and 2,000 Support messages. Choose `scenario` in
the manual workflow; only the following bounded shapes are accepted:

| Scenario | Products / services each | Town Hall rooms | Bargaining history | Marketplace history | Busy rooms |
|---|---:|---:|---:|---:|---:|
| standard | 100 | 2,000 | 20,000 | 10,000 | 0 |
| large | 1,000 | 20,000 | 200,000 | 100,000 | 0 |
| busy | 100 | 2,010 | 25,000 | 10,000 | 10 |
| large-busy | 1,000 | 20,010 | 205,000 | 100,000 | 10 |

Busy rooms each have one buyer and ten distinct admitted, paid providers. Each
provider reads and sends into its shared room on every cycle, with periodic
offers. These writes exercise the room lock and notification fan-out to ten
other members. The first 20 actors share two rooms, 50 share five, and 100 share
all ten. The runner verifies outsider access is denied and checks actual seed
counts, including full rooms, before starting traffic. It rejects a scenario
that differs from the database's seeded identity.

Historical marketplace messages are imported with only the history-revision
trigger temporarily disabled inside the seed transaction. Their equivalent
revision counts are then rebuilt and the trigger re-enabled. The runner refuses
fixtures unless revisions match history and the live trigger is enabled. API
traffic uses the normal triggers. This avoids treating a single huge historical
import transaction as live message throughput; seed duration is not benchmarked.

The workload exercises
catalogue/service browsing, authenticated buyer/provider history, both directions
of marketplace sends, Support history/replies, request creation and provider
offers/bargaining. Notification database triggers execute normally.
Public browsing omits Authorization so the normal shared public cache is tested;
private requests use valid signed synthetic sessions. A separate uncached stress
case is needed before estimating a predominantly authenticated-browsing workload.
Distinct synthetic client IPs exercise the current Redis rate limiter rather than sending
all clients through one shared-IP allowance. This is not a fix for production
proxy-header trust or a shared-IP/abuse qualification.

`smoke` uses 20 seconds per stage, `baseline` 90 seconds, at 20/50/100 active
buyer/provider pairs. The optional `database_cpus` input accepts only one
(default) or two CPUs for the disposable PostgreSQL container. It changes no
API, pool, memory or dataset limits, and the stage output records the ceiling.
This comparison tests whether the lab's database CPU ceiling limits throughput;
it is not a production CPU sizing formula.

Each scenario uses the same 20/50/100 active
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
Resource samples record container CPU/memory and PostgreSQL active connections
and lock waiters approximately every five seconds during the workload. API logs
also contain the normal cumulative pool-acquisition and admission counters.
The lab PostgreSQL logs statements slower than 100ms with bound parameter
logging disabled; these logs contain synthetic workload SQL only. Production
logging settings are not changed. Per-operation status counts distinguish
429/503 overload from other errors. Each stage reports whether all successful
p95 values met the initial 500ms target separately from the correctness result.
The workflow exit status enforces correctness and absence of overload, not a
500ms latency certification. Interpret latency against the launch runbook's
initial 500ms operating target and investigate each operation separately.

The lab puts all services and generator on one shared CI machine, with per-service
CPU/memory ceilings. Its results are a repeatable software baseline. They omit
mobile rendering, real internet/TLS/CDN, media transfers, worker queue drain,
production-scale catalogue history and production hardware/network
characteristics. Sampling can miss short utilization peaks; end-of-run Docker
stats are a snapshot. Further qualification needs worker queue-age observations
and staging on hardware representative of the main/database split.
Do not run this workload on a production host or against production databases.

No AWS or other paid resources are provisioned. The workflow consumes GitHub
Actions minutes under the repository's existing account plan and quotas. Cleanup
removes only this lab's Compose project and anonymous disposable database data.

## Independently paced burst and recovery

The manual `burst` profile keeps the same fixed isolated origin and synthetic
fixtures. It offers 60 requests/sec for 20 seconds, 1,500/sec for 20 seconds,
waits ten seconds for draining, then offers 60/sec for 30 seconds. Scheduling is
independent of response speed, with a hard 384-request generator concurrency cap
(above the replicas' combined 128 admission slots; below proxy maxconn 512).
Arrivals more than 50ms behind schedule are counted as scheduler drops. Slots
that cannot launch immediately are counted as generator-concurrency drops;
there is no client queue or catch-up retry. Every planned arrival is accounted
for as launched, scheduler-dropped or concurrency-dropped. These are requests,
not simulated people. Reported elapsed time includes draining pending requests.

JWTs/read templates are prepared before timing. The mix includes catalogue,
services, Town Hall, marketplace/Support history, both directions of marketplace
sends and admitted busy-room bargaining (or the buyer's room in standard data).
Each attempted write gets a fresh client ID and body; writes are never replayed.
All 100 synthetic identities rotate through the same mix. Normal Redis limits
stay enabled and synthetic client IPs are the same bounded addresses as baseline.

Expected overload is distinguished from correctness failure: a 429/503 must
include JSON error text and a positive bounded Retry-After. HTML proxy 503s,
500s, invalid successes and network/timeouts count as unexpected failures.
The spike must observe the actual admission middleware's 503 busy response;
rate limiting or generator drops alone do not prove admission protection.
Spike latency and generator drops are reported but do not masquerade as success.

Warmup/recovery must have zero errors, overload or generator drops, observe both
replicas, and meet every operation's 500ms successful-p95 target. A spike error
still proceeds to recovery so evidence is retained. A failed warmup stops before
the spike. Confirmed writes must match stored rows after the requests drain,
including on failures. A mismatch or failed recovery fails the workflow. Green
CI here proves only these explicit checks in this lab, not production burst
capacity, worker queue drain, server redundancy or physical autoscaling.
