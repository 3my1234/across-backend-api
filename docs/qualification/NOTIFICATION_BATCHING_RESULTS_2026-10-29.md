# Notification batching: isolated correctness and matched comparison

29 October 2026. Experimental only, no backend main merge or production changes.

Source `d5a854c2d00914b5db944f3f8bae8659599df134`, branch `experiment/launch-qualification`. Run https://github.com/3my1234/across-backend-api/actions/runs/40853697871 succeeded for controller regressions and matched ABBA benchmark. Artifact inspection showed receipt worker tests SKIPPED because RECEIPT_TEST_LOCAL was omitted. Corrected workflow source29ee9fa / run40854663425 requires that flag and rejects skipped required tests. Generator dispatch remains held until real worker tests pass.

## Matched results

Same synthetic10recipientroom, 100transactions per arm, PostgreSQL16 at2CPU/1GiB, unchanged pools/security, ABBA order original/batched/batched/original. Each transaction checks10notifications +10row-trigger enqueue probes, then rolls back. Includes correctness query and rollback overhead in client timings. No external delivery or business commit-rate measurement.

| Arm | Fanout SQL calls | Cumulative fanout SQL ms | Controller transactions/s |
|---|---:|---:|---:|
| 1 original | 1100 | 115.840 | 261.43 |
| 2 batched | 100 | 72.529 | 652.47 |
| 3 batched | 100 | 72.201 | 665.50 |
| 4 original | 1100 | 121.511 | 248.76 |

Mean cumulative fanout SQL 118.675ms -> 72.365ms (39.02% reduction), SQLcalls1100->100 (90.91% reduction). Mean controller throughput ratio2.58x. Sample is short and sequential, not wholeplatform throughput or concurrent500proof. pg_stat_statements elapsed time is not exclusiveCPU, and full DB pressure must still be measured under actual workload. Shared-buffer hits did not materially decrease; do not claim a measured databaseCPU reduction from this microbenchmark.

Controller tests passed: correct recipients/self/block/removal/privacy, duplicateeventdedup, rollback,140recipientbatchboundaries,fourconcurrentrequests,laternewrecipientsafterduplicatepages,finalpagefailure rolls backbusinessmessage/notifications/triggerprobe. ExistingTownHallreply/authorization/seatlimit/expiry/handoff passed; actualAPIbuild and nearbySQLhash guard passed.

Implementation uses64recipient INSERTSELECT batches in the existing business transaction. SELECTEDUUID cursor advances even on conflict-onlypages. No externalnetwork inside helper, no independentcommit, no unboundedGorecipientcollection. Batch sizes bound each statement, not total transactionduration under arbitrarily large populations; existing room-admissionlimits remain required. Noorderingcontract acrossindependentrecipients introduced.

Row-triggerprobe is not the fully migrated productionpush/emailtrigger chain. Separateworkerreceipt tests and lateractualstackqualification remain necessary. No fresh acknowledgedwrite-loss result or productioncapacity claim.

Raw artifacts and SHAmanifest: `qualification/notification-batching-20261029/40853697871/`. Next: actualworkerrecoveryPASS, longgeneratorqualification, controlleddatabaseRAM/actualsharedhostcalibration, unchanged500sustained onlyafterallprerequisitespass.

## Corrected no-skip gate: PASS

Run https://github.com/3my1234/across-backend-api/actions/runs/40854663425 at29ee9fa PASSED. Application helper unchanged fromd5a854c; workflow now opts in real isolated receipt tests and rejects skipped required tests. Independent artifact review verified noSKIP/noFAIL in both controller and worker logs. Actual receipt concurrent claim/crash recovery/fencing, bounded drain/eligibility, missing receipt/provider backoff, cancellation/device rejection and RetryAfter tests all executed and passed.

Matched ABBA rerun: original SQL80.871/80.880ms and1100calls per100transactions; batched57.174/57.286ms and100calls. Mean80.876->57.230ms (29.24% reduction), controller throughput2.37x. Across two short matched runs reduction29?39%, throughput2.37?2.58x; do not select only the faster run or attribute cross-run differences to code. All results remain microbenchmark scope; no500capacity or measuredDBCPU claim.

Long independent generator run40855200111 was authorized after this no-skip proof, exactsourceac854e5, currentlyRUNNING. Isolated memory-sized actual-stack calibration is prepared at844af84 but not dispatched. It requires successful1800secondgenerator artifact and unchanged qualified internal/migration/client source. DB RAM explicit1g/8g, default8g; CPU remains2core.8GiB RAM similarity is not four-vCPU/network/storage production equivalence.
