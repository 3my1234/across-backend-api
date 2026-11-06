# Messaging and Town Hall pool interference correction

Candidate application source: c2da06d1ddf715bd3904a56864c0a0667785b4df. No deployment, migration or live-host retry performed.

## Confirmed retained VPS evidence

Run42220057545: between12:49:47 and12:49:58UTC cumulative pool acquisition waiting rose from59ms to129104ms across two APIs. Empty-pool acquisitions rose from3 to1924. The sampled API group CPU use in ten-second intervals overlapping the burst was approximately36% of one CPU; test PostgreSQL approximately92% of one CPU (two-CPU cap). This does not exclude shorter CPU spikes. It does not support increasing the API CPU allocation as the first correction. Room-lock SQL accumulated191calls, mean77.95ms/max775.15ms; these SQL figures include both stages, not stage-only deltas.

The old send/offer handlers begin transactions before taking the shared room lock. Multiple writers to a hot room can therefore consume every pooled connection while doing no useful work. Unrelated messaging, inbox, presence and reads then queue for those connections. Pool pressure is confirmed; the share attributable to each contributor is not fully established by the sparse VPS observations.

## Correction

Per-controller, per-room channel scheduling before Begin for shared chat sends and Town Hall offers. Different rooms remain independent; UUID aliases normalize to one scheduling key. Waiters hold no database connection or transaction. Queue entries disappear when their last participant leaves; cancellation drops the reference; release is idempotent. Database FOR UPDATE, fresh membership/subscription/closure authorization, idempotency, notification triggers and commit remain unchanged. Other replicas, closure and membership writers still synchronize through the original PostgreSQL lock. No new rejection policy, timeout, pool expansion or relaxed acceptance gate.

## Verification

Real disposable database test:16 concurrent HTTP retries through two independent controllers and a four-connection shared test pool, while a separate transaction holds the room lock. At most two connections occupied by blocked writers; unrelated query completes. After release,16responses return one message ID, one stored message, exactly3notifications and3durable trigger records. Closed room still returns409. Cancellation, UUID aliases, independent rooms,100concurrent acquisitions, double release and entry cleanup tested. Full PostgreSQL16 release regression and race checks passed in GitHub42223272112.

Full synthetic fixture local100client diagnostic: two pools of8connections, ten hot-room providers and90independent private chats,1200requests over60seconds per arm. Real handlers and live triggers; authentication bypassed. Windows PostgreSQL18, no Linux CPU caps: these are diagnostic timings, NOT VPS qualification. All requests returned expected statuses. Four ordered arms: old,new,old,new; first old arm overlapped the end of separate schema regressions and is not a clean standalone baseline. The later old/new pair had no concurrent database tests.

| p95 milliseconds, clean repeated pair | Old | Candidate |
| --- | ---: | ---: |
| Town Hall offer handler |2212.8|1863.6|
| Town Hall send handler |632.0|503.0|
| Inbox handler |984.6|674.1|
| Private history handler |240.9|135.7|
| Private send handler |2249.5|1858.7|
| Town Hall offer pool acquisition |885.8|327.4|
| Inbox pool acquisition |880.3|485.7|

This deliberately synchronized burst still exceeds500ms on several operations. The scheduling correction demonstrably reduces interference but is not claimed to remove all transaction cost or prove100/500capacity. No standalone uncached catalogue query was included in this diagnostic. The completed original authenticated100-session mixed k6 replay is reported below; its unchanged workload differs from this deliberately synchronized diagnostic.

## Unacknowledged write

The sole unmatched OFFER key in the VPS k6 log was emitted at12:50:08UTC, at the abort boundary (client reference5a216533-6f62-4168-a914-53414b63b51a). Independent verification recorded218stored/217acknowledged, no duplicate/missing/unknown keys. This is consistent with an in-flight-at-abort response; retained evidence does not prove response delivery. The concurrent retry regression demonstrates safe replay semantics, not a replay of the deleted original VPS database row. Reconciliation remains strict.

Evidence: qualification/messaging-pool-{before,after,before-repeat,after-repeat}/, messaging-pool-regressions.log, messaging-pool-ci-42223272112.log and live-42220057545/. No new VPS operator command is authorized or needed during this work.

Validation references: PostgreSQL16 regression/race run https://github.com/3my1234/across-backend-api/actions/runs/42223272112 passed. Authenticated100-session disposable replay https://github.com/3my1234/across-backend-api/actions/runs/42223698018 passed; execution harness787fab9, candidate APIc2da06d. Original stage policy checked independently:100constant VUs,180seconds, think3-12seconds,500ms/1500ms latency,0.5% error cap,15second polling; no source or threshold rewrite.

## Final authenticated disposable replay: PASS

GitHub42223698018 completed successfully. Candidate c2da06d executable SHA2563249aa64c43eccbbdb2fb60b04fe9892bf3187d1ce74cfbfdaab33fe01f713c4 verified on both running API replicas and downloaded binary. Original generated k6 source checksum unchanged. Full500pair large-busy fixture,100authenticated concurrent sessions for180seconds; app group1CPU/2.5GiB, PostgreSQL2CPU/3GiB, generator2CPU/4GiB. No change to request mix, hot-room selection, polling cadence or acceptance thresholds.

- Overall successful-request p95 **7.21ms**, p99 **30.67ms**;9,361measured requests, zero unexpected errors.9,565HTTPrequests including setup. These are disposable-run measurements, not VPS predictions or a before/after VPS comparison.
- Private message send p95 **12.06ms**, Town Hall offers **10.64ms**, Town Hall send **7.38ms**, inbox **9.34ms**, catalogue **44.81ms**, service browsing **25.65ms**. Every original per-operation threshold passed.
- **1,513offered =1,513acknowledged =1,513stored**, zero missing/duplicate/unknown/unconfirmed writes; normal completion marker present.747private messages,487Town Hall messages,279bid messages;740receipts. Current bids and driver states independently reconciled;10providers exercised presence, not100drivers. Post-run intended-peer visibility passed for747private messages.
- Cleanup completed. Queue recovery preflight passed; mixed-workload worker drain and external phone/email/push delivery were NOT qualified.
- No production service, database, DNS, resource allocation or threshold changed; no VPS run or new live review started. Owned local diagnostic database stopped after measurements.

Decision: candidate correction and100-session disposable acceptance PASS. The pathological synchronized diagnostic remains above500ms on some operations, so no claim that every burst pattern is solved. Actual VPS100/500capacity remains unqualified until the candidate is tested there. The old reviewed VPS attempt is consumed and must not be rerun. Reuse this verified binary for the next explicitly authorized isolated VPS checkpoint; retain original20?100?ramp500?30minutes500gates and caps. No further catalogue rewrite, pool enlargement or deployment is justified automatically.

Machine-readable result: qualification/RESULT_MESSAGING_100_42223698018.json. Raw evidence: qualification/messaging-100-42223698018/. Checksums: qualification/messaging-100-42223698018-sha256.json. Release regression and race proof:42223272112.
