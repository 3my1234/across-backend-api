# Receipt capacity and recovery qualification

28 October 2026. **Passed the isolated 100-session receipt milestone.** This is not production or 500/1,000-session qualification. No merge, production migration/deployment, VPS purchase or APK release.

Exact sustained source: `d72df8b1f205886f9b03db889c79d5359d9628ec`, experimental branch `perf/receipt-throughput`. [Sustained run 40627566223](https://github.com/3my1234/across-backend-api/actions/runs/40627566223). API executable SHA-256 `e7968d54414718997881c215dbb5d86e11f39f705801c58600559c5c25cf5a3a`; lab executable `68903718dffbdc781b82bf0843bb6e76f1f05f56c6309bce167bc90e829ae91a`.

## What changed

Experimental migration 108 adds receipt-only ownership tokens, 60-second leases, retry scheduling and a database-wide dispatcher gate. Receipt HTTP runs outside SQL transactions; terminal updates require the current owner, ticket, status and live lease. Missing receipts back off instead of starving later work. Provider failures apply shared backoff and honor longer valid Retry-After values.

The receipt loop can drain five batches of 100 per 15-second tick under a 12-second budget. Across worker replicas, the gate permits one receipt HTTP request in flight and at least one second between request starts. The minimum 15-minute eligibility rule, send batches, send retry policy, message authorization and password processing are unchanged.

## Matched policy comparison

[Final regression/comparison run 40626904902](https://github.com/3my1234/across-backend-api/actions/runs/40626904902) passed real-PostgreSQL/race ownership, recovery/fencing, cancellation, missing-receipt, provider-backoff, device rejection, existing push-send, worker/config/lab, migration and API/worker build checks.

Original/candidate/candidate/original arms used copies of identical 1,000 eligible rows and token identities, the same provider mock and actual 15-second tick timing. The historical function was copied from `3600673b54c1674675ffccf696ed9df21edaf0ca` into a test-only baseline.

| Policy | Completed/stored receipts per 30.002-second arm | Checks/second | HTTP requests |
| --- | ---: | ---: | ---: |
| Original, both repetitions | 200 | 6.666 | 2 |
| Candidate, both repetitions | 1,000 | 33.331 | 10 |

This proves approximately fivefold receipt scheduling capacity in this small policy comparison. It is not fivefold whole-platform throughput or a CPU-efficiency comparison.

## Sustained HTTP and correctness results

The same 100-established-session, 30-minute mixed HTTP/polling profile and unchanged latency, error and integrity gates were retained. All 100 identities authenticated gradually before measurement. Actual VU evidence contains 1,800 samples at 100 VUs and approximately 1,800 seconds of dwell. These are authenticated sessions with think times, not 100 simultaneous sign-ins or 100 requests permanently in flight.

| Measurement | Result |
| --- | ---: |
| Offered = completed measured requests | 81,186 |
| Request-window throughput | 45.00 requests/second |
| HTTP error rate / dropped iterations | 0 / 0 |
| Overall p95 / p99 | 24.62 / 75.66 ms |
| Worst operation p95 | Services: 67.91 ms |
| Worst operation p99 | Inbox: 297.98 ms |
| Offered = acknowledged = stored message keys | 11,995 |
| Missing / duplicated / unknown / unconfirmed message keys | 0 / 0 / 0 / 0 |
| Marketplace / Town Hall messages | 7,292 / 4,703 |
| Marketplace messages visible to intended peers afterward | 7,292 across 200 history pages |

This legacy comparison mix does not submit Town Hall bids or driver GPS/nearby operations. Its `stored_offers` count is zero. The separately prepared expanded harness must qualify those operations before 500/1,000-session claims.

## Notification and recovery results

- All 3,698 unique email keys reached worker completion and had mock-provider acknowledgement evidence.
- All 32,696 unique native push records reached worker completion and mock receipt confirmation. Final native state is `delivered` for all 32,696; eligible receipt backlog and total outstanding receipt count are zero.
- All 42 send jobs owned at the injected worker SIGKILL recovered. The first approximately 30-second recovery snapshot remained zero; the later snapshot showed all recovered, preserving the existing five-minute send lease.
- The 25-second mock SMTP451/native503 outage and lost-acknowledgement scenarios remained enabled. Peak sampled send backlog was 60 emails and 570 native push records; maximum sampled ages approximately 302/303 seconds include the deliberate five-minute lease-recovery case.
- Send backlog was already clear after load/recovery checks: drain check timestamps 04:11:19–04:11:21 UTC do not mean the injected outage drained in two seconds.
- Real-time receipt-tail observation ran from approximately 04:11:23 to 04:25:15 UTC. It began with 15,035 pending receipts, mostly not yet eligible. During that tail, peak eligible backlog was 292 and maximum sampled age beyond eligibility was 12.23 seconds. It ended with zero pending, eligible or claimed receipts.
- Across the retained mock journal, latest generated provider acknowledgement to first mock receipt averaged 906.69 seconds and peaked at 913.55 seconds. These are provider-journal approximations, not exact database `sent_at` measurements or handset delivery latency. The unchanged 900-second minimum was not accelerated.

Provider acknowledgement means a response was generated at the mock, not proof that the client received it. Email provider acceptance events were 3,703 and acknowledgement-generation events 3,702 for 3,698 unique keys; native acceptance events were 32,767 and acknowledgement-generation events 32,696 for 32,696 keys. Repeated acceptance affected five email keys and 71 push keys under intentional faults. Message storage was exactly once; external delivery is not claimed to be exactly once. No real email, Expo/FCM/APNs notification or payment was sent.

## Resources and limits

Runner: four-vCPU AMD EPYC 7763 host. PostgreSQL remained limited to two CPUs/1 GiB; APIs to 0.75 CPU/512 MiB each with eight-connection pools; worker to one CPU/512 MiB and four connections; generator to one CPU/512 MiB; mock to 0.25 CPU/128 MiB. Same approved isolated architecture, not the production VPS pair.

PostgreSQL averaged 25.89% of one CPU core, peaking at 72.59% in approximately five-second samples; the database quota was 200% of one core. Worker averaged 1.28%, mock 3.27%, generator 7.99% (peak 18.57%). Sampled lock waiters were zero and Redis memory peaked at 3,261,272 bytes.

API pools recorded 2,206 empty acquires across 261,575 acquires and 62,521 ms cumulative wait across both pools; no cancelled acquires. The cumulative wait is not a 62-second individual request. Pool waits and SQL cost need attention at larger stages. This sustained run is separately seeded and uses a different runner from older experiments: do not attribute differences in CPU or latency solely to the receipt fix. Only the short policy comparison above used matched copied data in alternating order.

CPU and queue observations are sampled, not a continuous trace. During load, the legacy observer labels native sent age using parent notification creation time; exact eligible-age measurements above cover the post-load tail. The expanded observer now adds actual eligible-age samples during its future workload. Actual SQL timing attribution is also prepared for that expanded sustained stage.

## Provenance and next gate

Initial run 40626362748 was cancelled after identifying that the lab worker/mock's 40-minute lifetime could not cover 30 minutes of load plus a real receipt-eligibility tail. Partial evidence was retained; no capacity result is claimed from it. The bounded lab lifetime was corrected to 65 minutes and all checks repeated successfully before the final campaign. Production lifetimes/configuration were not changed.

Raw evidence: `qualification/receipt-throughput-20261028/`; independent analysis: `sustained-analysis.json`, produced by `qualification/analyze_receipt_campaign.py`, adapted from the retained prior worker analyzer. Earlier successful and failed evidence is preserved.

Next: the expanded 100-session HTTP pilot, then expanded sustained qualification. Actual bid messages must reconcile separately from the mutable current-offer table; driver transition acknowledgements and final mutable presence must reconcile; nearby filtering/order/privacy and nonzero operations must pass. Keep 500/1,000 gated on that coverage and resource evidence. The receipt scheduler still has explicit throughput ceilings; these results do not establish capacity at larger arrival rates.

K3s files are preparation drafts only. Production release requires separate migration compatibility/backup/rollback review and user confirmation. Old receipt workers do not understand the new dispatcher gate; an eventual cutover must not assume old and new receipt consumers coordinate. Real vendor limits/handsets, web push, full worker entrypoints/other roles, media transfer, production networking/hardware and broader recovery remain unqualified.
