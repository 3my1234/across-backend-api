> WITHDRAWN: the user has prohibited additional paid testing resources. Historical proposal only; do not act on its approval request. Use EXISTING_VPS_QUALIFICATION_PLAN_2026-10-29.md.

# Temporary calibration lab ? approval proposal

Prepared 29 October 2026, Africa/Lagos. **Pending explicit approval. Nothing has been provisioned.**

## Available resources

Read-only checks found zero registered self-hosted runners for the backend repository. Its owner is a personal GitHub account; larger runners require an eligible organization. The laptop has four logical CPUs and 7.9 GiB RAM. The current standard Actions runner has four CPUs. Both existing VPS instances remain excluded because they run production. No suitable connected, free or already-authorized environment was found.

## Exact proposed resource

One temporary **Hetzner CCX43**, EU/Nuremberg subject to availability: **16 dedicated x86 vCPUs, 64 GB RAM, 360 GB NVMe, Ubuntu 24.04 LTS**. One Primary IPv4, restricted operator SSH, no public application or database ports. No backups, snapshots, additional volumes, load balancer or K3s installation.

The provider defines a dedicated vCPU as one physical CPU thread, not necessarily a whole physical core. Provider terms, live resource inventory and CPU affinity must all be verified. Container quotas alone are insufficient.

| Workload | CPU set | CPU quota | RAM ceiling |
|---|---|---:|---:|
| PostgreSQL | 0?3 | 4 | 8 GiB |
| Load generator | 4?5 | 2 | 4 GiB |
| API 1 | 6 | 0.75 | 512 MiB |
| API 2 | 7 | 0.75 | 512 MiB |
| Gateway | 8 | 1 | 128 MiB |
| Notification worker | 9 | 1 | 512 MiB |
| Redis and mock transport | 10, explicitly shared | 0.25 each | 128 MiB each |
| Observer/exporter; seed/verifier in separate phases | 11 | 0.5 for observer | 256 MiB for observer |
| Host, controller, build and artifact work | 12?15 | Reserved | Remaining RAM |

Critical workloads have non-overlapping CPU sets. Only the two auxiliary services intentionally share one slot; their combined quota is 0.5 CPU. The minimum placement budget is 12 logical CPU slots, plus host headroom. Measured services have about 14.1 GiB of RAM ceilings; require at least 20 GiB available before startup. A combined host with at least 24 GiB RAM is the minimum planning envelope; the selected provider SKU has more.

The prepared `dedicated-compose.yml` is unused. Its offline validator accepted the allocation and rejected overlapping CPU sets, insufficient CPUs and insufficient RAM. **These tests do not verify live CPU dedication or qualify capacity.**

On the new VM only, configure a cgroup-v2 workload slice for CPUs 0?11 and place host/controller work on 12?15. Verify effective root, parent and container CPU sets; quota ancestry; Docker CPU/RAM settings; host memory/disk; CPU steal and throttling. Do not constrain Docker's parent so it cannot delegate workload CPUs. Kernel and hypervisor interference still require measurement. The earlier process-affinity-only preflight must distinguish the controller's intentional four-CPU reserve from the complete host allocation.

## Cost and duration

The official EU rate table lists **US$0.5216/hour** for CCX43, excluding tax and IPv4. Six rounded hours cost **US$3.1296 in compute**, approximately **US$3.15 before tax and the small IPv4 charge**. Primary IPv4 is separately billed; the published EUR monthly rate is EUR0.50. Verify the current account checkout quote before creation.

Requested approval: **one VM plus one IPv4, up to six hours, total usage budget US$10 including applicable tax/IP charges**. Expected work is two to three hours; six hours allows setup, collection and recovery. No automatic extension, second server or larger SKU. Stop for additional approval if a deposit, setup fee, minimum charge, account requirement or checkout quote falls outside this scope. Account eligibility and stock are not yet established.

A budget alert is not a provider-enforced spending cap. Turning the VM off does not stop billing. Confirm an external expiry/deletion monitor before creation. Delete the VM and its detached IPv4 after collection or at the six-hour deadline, verify both are absent, and report any cleanup failure immediately while retrying. Provider API credentials must remain off the test runner. No cost guarantee is claimed if external deletion fails.

## Setup and execution after approval

1. Verify checkout, dedicated CPU class, region, account eligibility and budget. Record the exact new resource IDs, creation time and six-hour expiry. Establish external deletion monitoring and billing alerts. Restrict SSH to the operator.
2. Install Docker Compose and the required pinned build tools on the new VM only. Use a single-use controlled CI job pinned to reviewed source, with no automatic PR execution, production secrets or production network access. Verify the allocation above before load. Synthetic fixtures and mock email/push/payment transports only.
3. Requalify the original 500-session generator for 30 minutes at two CPUs/4 GiB on this host. Preserve client hashes and all original duration, accounting, memory, CPU and correctness gates. Place the synthetic target outside the generator and database CPU sets and record its budget.
4. Run the bounded 180-second application calibration with four database CPUs/8 GiB and one gateway CPU. Keep API CPU quotas, pool sizes, admission limits, workloads, latency/error thresholds, authorization, idempotency, write reconciliation and recovery checks unchanged. Retain per-operation latency, SQL, queues, resource and generator headroom evidence.
5. Only after independent review of passing calibration evidence, run the unchanged 30-minute 500-session application stage, full write reconciliation and real receipt-tail/drain checks. Stop on failure. No 1,000-session stage. The rejected sender candidate is not applied; application and migration source remains the previously tested batching candidate.
6. Download and hash evidence, update the master handover, unregister the single-use runner, delete only the recorded temporary resources, verify deletion and report cost.

This is a CPU/RAM calibration environment, not proof of identical production storage, networking or hardware performance. It does not change either Contabo host, Coolify, the unrelated website, PostgreSQL/Redis, DNS, K3s or mobile.

## Official sources checked 29 October 2026

- [CCX43 specifications](https://www.hetzner.com/cloud/general-purpose/)
- [Published hourly rates](https://docs.hetzner.com/general/infrastructure-and-availability/price-adjustment/)
- [Dedicated vCPU definition](https://docs.hetzner.com/cloud/servers/faq/)
- [Primary IP pricing](https://docs.hetzner.com/cloud/servers/overview/)
- [Billing, deletion and alert limitations](https://docs.hetzner.com/cloud/billing/faq/)
- [GitHub larger-runner eligibility](https://docs.github.com/en/actions/how-tos/manage-runners/larger-runners/control-access)

**Next decision:** approve this temporary lab and its creation/deletion budget, or supply an already-owned isolated host for read-only suitability review. The user's explicit restriction on paid provisioning requires that approval before creation.
