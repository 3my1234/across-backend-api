# Cloud scaling proposal — approval required before provisioning

Prepared 16 October 2026. This is a planning estimate, not an AWS quote or
capacity guarantee. No resources have been created. Prices are USD, before tax,
with 730 hours/month, Linux/x86 on-demand compute, no credits or annual contracts.
Use US East (N. Virginia) published compute rates as the comparison reference.
Choose the actual region after latency tests from Nigeria, then recalculate its
prices before approval. Do not assume US pricing applies to every region.

## Recommended architecture

CloudFront Business with WAF/private-origin protection → ALB → ECS Fargate API
service in two availability zones. Two 1-vCPU/2-GiB API tasks stay warm; propose
an initial limit of ten tasks after staged load tests and connection-budget checks.
One separate 0.5-vCPU/1-GiB worker initially. Managed PostgreSQL with standby,
managed shared Redis, private networking, S3 media, monitoring and tested backups.
The existing Coolify services remain the rollback deployment during cutover.

CloudFront's CDN does not make private chats cacheable. Protected API requests
must still reach the origin; media and safe public responses can use edge caching.
CloudFront request allowances are per month, not simultaneous-user capacity.

## Baseline monthly planning envelope

| Component | Assumption | USD/month |
| --- | --- | ---: |
| API compute | 2 × 1 vCPU/2 GiB, 730h | 72.08 |
| Worker compute | 1 × 0.5 vCPU/1 GiB, 730h | 18.02 |
| Managed load balancer | base + modest capacity usage allowance | 25–50 |
| PostgreSQL | small managed deployment with standby/storage allowance; size after tests | 150–300 |
| Redis | small managed replicated cache allowance; size after tests | 40–80 |
| Private network | two-zone NAT, IPv4 and modest network usage allowance | 80–140 |
| Monitoring/logging | bounded retention and low initial volume allowance | 15–30 |
| CloudFront Business | published flat-rate plan | 200 |
| **Subtotal** | before variable media/backup/email charges and tax | **600–890** |

Recommend planning **$600–$1,000/month** for this initial cloud foundation.
PostgreSQL, Redis and network figures are **engineering allowances**, not
configuration-specific published quotes. Final sizing, region-specific calculator
estimate and migration overlap must be approved before purchasing. Existing
Contabo bills continue until deliberately retired; they are not included here.
Also exclude engineering fees, SMS, Flutterwave fees, large media storage/requests,
extra backup retention, high data transfer and large-scale load-test charges.

Published reference compute: $0.04048/vCPU-hour + $0.004445/GiB-hour.
One API task is (0.04048 + 2 × 0.004445) = $0.04937/hour, about $36.04/month.
ALB reference pricing is $0.0225/hour plus $0.008/LCU-hour; actual LCU consumption
depends on connections, bytes and rule processing. It grows with traffic.

## Illustrative burst compute costs

Only additional API compute above the two warm tasks is shown; **database, cache,
load balancing, logs and traffic are extra**. These rows are arithmetic examples,
not evidence these task counts can serve a particular number of users.

| API tasks running | Extra compute/hour | Extra for 24 hours | Extra if sustained 730h |
| --- | ---: | ---: | ---: |
| 10 | $0.395 | $9.48 | $288.32 |
| 20 | $0.889 | $21.33 | $648.72 |
| 100 | $4.838 | $116.12 | $3,531.93 |

Ten tasks is an initial operational ceiling, not permission to scale to 100.
At very high demand, PostgreSQL, Redis, message fanout, bandwidth or service
quotas can become the constraint first. Genuine million-user concurrent demand
needs separate workload modelling, load testing and a materially larger budget;
no responsible total can be quoted from user count alone.

## Spend and capacity controls

Use explicit API/worker maxima, maximum DB/cache sizes, log retention and quotas.
Create budget notifications at 50%, 80% and 100%, plus rapid-spend/anomaly alerts.
Budget notifications do **not** enforce a real-time dollar cap: billing is delayed,
and requests/network/database charges can accrue even at the task-count ceiling.
Overload handling must provide graceful busy responses once safe capacity is
reached. Do not promise both unlimited service and a fixed maximum bill.

Before activating the proposal, approve the regional itemized quote, maximum
task counts and response to reaching the ceiling. Perform synthetic staging
load tests, multi-zone/failover/restore tests and migration rehearsal. Push-driven
updates and bounded delta sync remain necessary to reduce polling amplification.
Do not migrate or redirect live traffic just because these documents exist.

Official pricing/behavior references checked for this proposal:
- https://aws.amazon.com/fargate/pricing/
- https://aws.amazon.com/elasticloadbalancing/pricing/
- https://aws.amazon.com/rds/postgresql/pricing/
- https://aws.amazon.com/elasticache/pricing/
- https://docs.aws.amazon.com/PricingPlanManager/latest/UserGuide/pricingplanmanager-ug.pdf
- https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/flat-rate-pricing-plan.html
- https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service-auto-scaling.html
- https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-best-practices.html
