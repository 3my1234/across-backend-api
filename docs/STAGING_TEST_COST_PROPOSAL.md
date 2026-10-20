# Proposed temporary staging run — approval not granted

The four-session CI smoke uses existing GitHub Actions allowances. No paid
machine has been created, and neither current production server is a load target.

## Resource envelope for a separately approved larger run

| Temporary role | Planning shape | Initial hourly quote ceiling |
|---|---|---:|
| Application staging | 4 vCPU / 8 GiB; API replicas, Redis and worker | US$0.25 |
| Database staging | 4 vCPU / 8 GiB; synthetic PostgreSQL | US$0.25 |
| Separate generator | 8 vCPU / 16 GiB; k6 only | US$0.50 |

These are proposed spending ceilings, not verified provider prices or a promise
that the generator supports 12,000 VUs. Obtain the actual regional hourly quote
before provisioning. Require non-burstable CPU or record burst credits/throttling.
Initial allocation is eight hours total: setup/seed, gated 100–12,000 ramp, a
30-minute sustained run if healthy, up to two-hour soak, recovery and cleanup.
No stage advances after failure; excess time is not permission to spend more.

Compute envelope is at most US$8 (8h × US$1/h). Reserve US$17 for disks, IPs,
registry/storage/requests, monitoring and modest network traffic: **proposed total
cap US$25 before tax**. This is an estimate and operational stop budget, not a
cloud-enforced billing cap. A media-heavy plan must separately estimate object
bytes and traffic before approval; it is not automatically covered by this cap.
No production migration, NAT gateways, managed databases or standing idle
foundation are included. Existing Contabo payments remain separate.

For reference, AWS bills public IPv4 per hour (currently US$0.005/address/hour),
and network/storage can add charges independently of requests or active users.
Actual provider, region, quote and cleanup ownership must be entered into the
approved plan before any paid API call. Do not assume existing free-tier credit.

## Execution prerequisites

- Complete the isolated storage and worker/media test transports and external
  generator deployment route; current smoke accepts only internal gateway origin.
- No production secrets, email/SMS recipients, device endpoints or bucket objects.
- Synthetic data only; separate object prefix/bucket, sandbox subscriptions and
  endpoints; maximum media test volume agreed before execution.
- Generator resource telemetry and achieved VUs/RPS; scale the generator only
  within a new reviewed cost envelope. Attribute generator saturation separately.
- Same limits/pools as the intended application tier; log immutable software
  revisions, CPU model, machine/container limits, duration, all workload weights.

## Cleanup procedure to prepare before provisioning

Tag every created resource with one unique run ID and an expiry time. Maintain
an explicit resource inventory. At expiry or abort: stop traffic; collect and
redact evidence; verify each machine/volume/object resource belongs to that run;
terminate only those instances; delete their disposable volumes, addresses,
registry images and test objects; confirm there are no surviving run resources.
Do not delete or stop production machines, backups or the cancelled AWS stack
without a separate checked scope. Confirm billing/resource cleanup afterward.

Required approval fields: provider/account/region, exact machine types and
hourly quotes, maximum eight-hour window, US$25 pre-tax cap, object-byte/traffic
limits, run ID, monitoring owner and cleanup deadline. Nothing in this document
constitutes user approval or an executed large-load result.

Pricing references checked 20 October 2026:
https://aws.amazon.com/vpc/pricing/
https://aws.amazon.com/ec2/pricing/on-demand/
