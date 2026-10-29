# Actual notification-trigger and CPU comparison ?29October2026

Run40877066781 PASS at886599f. Full migrated synthetic large-busy fixture, actual communityNotificationBatchSQL extracted from controller constant, one message UUID bound per statement via PREPARE/EXECUTE, ten recipients, every actual notification trigger and FK enabled. Eight parallel clients in a separately capped container,20seconds per arm; CPU order2/4/4/2,8GiB DBceiling. APIs/workers/generator stopped. No production or actual500session workload.

|DBCPU|Transactions|TPS|Mean transaction ms|
|---|---:|---:|---:|
|2|7206|359.609|22.246|
|4|12891|645.082|12.402|
|4|12794|640.233|12.495|
|2|8436|422.183|18.949|

Alltransactions zero failures, all writes rolled back; notification/native/web delivery counts matched before/after. These include PREPARE/DEALLOCATE/network/rollback overhead, not pure INSERT timing, commit durability, business-message transaction/row-locking or worker delivery. Four-core CIhost is shared with the small SQL client; not a guaranteed dedicatedfourcore DB or production hardware equivalence. ABBA helps with cache/order bias; no whole-platform TPS claim.

EXPLAIN ANALYZE single query9.73?10.43ms. Ten notification_sender trigger calls3.65?3.87ms; native enqueue2.31?2.49ms; web enqueue1.26?1.33ms. Recipient selection uses chat_participants index, about0.05ms, and is not the main cost in this plan. Timings include instrumentation overhead and FK/trigger work; cumulative parent/child timings must not be summed as exclusiveCPU.

Concrete candidate: ordinary user sender snapshot in migration100 unnecessarily includes provider join/UUID/member processing even when sender_is_provider is nottrue. The isolated sender-candidate.sql branches to a users-only indexed lookup for ordinary senders; provider branch keeps active-membership gate, name/avatar fallback, UUID handling and output shape. No production migration added. Matched original/candidate/candidate/original comparison40879057063 at526bed4 is RUNNING,2CPU/8GiB fixed, with4504result-equality cases per arm and realtriggers. No benefit claimed yet.

Next decision depends on that comparison: adopt only if sender snapshots match and full-trigger measured cost improves. Gateway1CPU vs0.25CPU has separately measuredlatencybenefit, but no allocation change has been applied to an actualapplicationcalibration. Full500/1000 remain blocked. MainVPS/Coolify/unrelatedsite,externalPostgreSQL,Redis,DNS,K3s/mobile untouched.

Evidence:qualification/trigger-profile-20261029/40877066781/artifacts(50files),analysis.json,SHA256SUMS.json. PostgreSQL reference:https://www.postgresql.org/docs/16/using-explain.html (trigger timings and EXPLAINmeasurement caveats).

Coverage clarification:session fixture creates native push tokens but no browser web-push subscriptions. Web trigger invocation is real, but its timing here covers no-subscriber lookup, not browser enqueue/delivery. Candidate identity equality is helper-output proof, not end-to-end browser notification delivery.
