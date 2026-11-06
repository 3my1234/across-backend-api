# Reviewed staged 500-session VPS qualification

Status: PREPARED ? LIVE EXECUTION NOT AUTHORIZED OR STARTED.
Run: `cohost-a8379918676f430a`. Review directory: `C:\Users\EMMA\Desktop\gouwu\qualification\singlehost-review\cohost-a8379918676f430a`.
Frozen execution source: `d3cf7ceea5f4c0a122df57f5883804c1a1128fe7` on `experiment/approved-singlehost-d3cf7ce`.
API source: `395ef241b9a35be9712cb6fc7d107bfe8be85f38`.
API SHA-256: `c8aa65b6d9618a4a8698616b0370d045d94dd8d1077041051e4ebeb502863c63`.

## Completed evidence

- Current full-fixture mixed 20-session/180-second application checkpoint PASSED: https://github.com/3my1234/across-backend-api/actions/runs/42210541636
- Exact-current-source coordinator/restricted-SSH/HTTP-forward/watchdog fault-stop/systemd hard-stop cleanup rehearsal PASSED: https://github.com/3my1234/across-backend-api/actions/runs/42216042490
- The rehearsal's executable hash is identical to the passing mixed test. Both API processes are checked via `/proc/1/exe` before load.
- Original generator proof42178904513 and original generator bytes/gates remain unchanged.
- Launcher/approval-file regression corrected and covered by 35 local tests (3 platform skips); exact-source Linux rehearsal passed.
- Review file hashes, API hash, snapshot hash/age/inventory and frozen published ref verified locally. No new temporary key or live GitHub trial secret exists yet.

## Sequence ? original reviewed policy, not the proposed alternate schedule

| Stage | Sessions | Duration |
|---|---:|---:|
| Baseline | 20 | 45 seconds |
| Baseline | 100 | 180 seconds |
| Gradual ramp | up to 500 | 180 seconds |
| Sustained qualification | 500 | 1,800 seconds |

Advance only if the existing performance, correctness, accounting and safety gates pass. No 250-session stage or new five-minute stages were silently added. Scheduled stage traffic totals36m45s; baseline, restore and receipt verification add time. Server-side hard limit remains120minutes. Stop at the first failing stage, preserve partial evidence, reconcile and clean up. No automatic second attempt.

## Resources and isolation

| Resource | Limit |
|---|---|
| Application test group | 1 CPU / 2.5 GiB |
| Isolated PostgreSQL | 2 CPUs / 3 GiB |
| Aggregate on database VPS79.143.176.173 | 3 CPUs / 5.5 GiB |
| Qualified free GitHub generator | 2 CPUs / 4 GiB |

CPU quotas are limits, not reserved independent cores. The main VPS hosts no new test resources. Existing production PostgreSQL/Redis/data/Coolify services/DNS/firewall/SSH authentication configuration are unchanged. Synthetic accounts, separate PostgreSQL container/database, Redis, bridge and storage only; no public test ports. Generator connects through restricted SSH forwarding to the private test gateway; database ports are not exposed publicly.

The existing full snapshot `cohost-ad09262dac9a4cdd` and image archive are reused and checksum checked; the new API executable and tools are uploaded. If a required cached image is absent, the existing supervisor can load it from the verified existing archive under its independent guard; no image/archive download or new infrastructure is purchased. No production configuration or credentials are copied into the test.

## Preflight and stop conditions

Startup independently checks the latest consumed attempt46f3dc98f1c94056 is cleaned up, checks no other test containers remain, verifies retained observer/snapshot/binary integrity, performs the existing five-minute ATL Xpress health baseline and checks current resource headroom before test creation/load. Live host state has NOT been queried during this preparation; these are mandatory just-in-time gates, not assumed passes.

Keep original p95<500ms and p99<1,500ms overall/per-operation, unexpected-error rate<0.005, no uncontrolled overload, valid content and exact independent write reconciliation. Do not relax them to advance.

Independent local watchdog aborts on available RAM<2GiB, disk free<20GiB, OOM, unhealthy protected production containers, CPU>85% for5seconds, IOwait>10% for5seconds, two consecutive public health failures, or three consecutive public-latency breaches. HTTP request limit remains2s, observer total2.5s, existing DNS-only bounded retries remain. Guard lease must be fresh within5s. Public probes are ATL Xpress API/readiness and atlxpres.com, not the unrelated website. The120-minute systemd hard stop and exact-owned-resource cleanup survive coordinator disconnection.

## Cleanup and approval command

Normal success/failure cleanup removes only this run's recorded/labelled test resources and temporary SSH key. Operator removes the temporary GitHub secret. The one-command launcher then requests a final local SSH password for a separate READ-ONLY audit: zero test containers/networks/volumes/key lines, stopped owned units, cleanup markers and protected production/API health. Audit failure is reported; it does not silently authorize another run. Cached images/snapshot and evidence files are deliberately retained.

Approval is required because the following command authorizes creating isolated resources and generating load on the live host. To approve this exact original staged procedure and execute it:

```powershell
& "C:\Users\EMMA\Desktop\gouwu\qualification\Start-Reviewed500.ps1" -ApproveTrial
```

Enter SSH passwords locally, keep the window open, and use the printed GitHub link for stage progress. The same command performs the final read-only audit; do not launch another copy. Without `-ApproveTrial`, or with stale/consumed/changed review files, it stops before SSH/setup.

## Interpretation

A pass proves only the tested synthetic workload/build/resource limits and isolated database-VPS placement. It is not production-deployment approval, a complete two-host production-topology certification, a simultaneous-signup/media-upload test, actual handset GPS/device-push/payment verification, or an unlimited-user guarantee. 500-session capacity is not qualified until the complete final stage and reconciliation/safety requirements pass.
