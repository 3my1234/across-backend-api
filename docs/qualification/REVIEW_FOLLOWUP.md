# Follow-up to the disposable-versus-VPS review

Backend remains frozen at `baa03e4aea43db055c2a0e7622e20dc047a1f23a`, tested executable SHA256 `5c42473a27a48d3c003d4047d438c31005907fbca2cf85f7a7e1e51f54a7fb16`. No new backend optimization, VPS setup/load, production deployment or migration.

## What the disposable result proves

[Exact hardened 100-session replay42245254946](https://github.com/3my1234/across-backend-api/actions/runs/42245254946) establishes regression acceptance for the actual original authenticated mixed workload and unchanged caps:100sessions/180seconds, p958.33ms,9361measuredrequests,1513offered/acknowledged/storedwrites, no unexpected errors or controlled overloads, full cleanup. It is not evidence that VPS100 latency has been fixed.

The failing VPS395ef24 build has NOT been replayed at100sessions in this same disposable k6 configuration. We therefore cannot claim the8.33ms replay distinguishes that historical failure. Separate matched local100client diagnostics and the controlled row-lock/pool-isolation database regression support the scheduling correction; their scope is documented in MESSAGING_POOL_CORRECTION.md and ROOM_QUEUE_REVIEW.md. The pathological synchronized local diagnostic remained above target for some operations and was not declared a qualification pass.

Two factual corrections: both disposable and VPS load paths include HAProxy. Actual isolated VPS workload travels from GitHub through an HTTP-only SSH forward into the private test gateway; it does NOT travel through the public Cloudflare hostname. Public production-health probes are separate. The previous100-session stage was constant100VUs and aborted after16.77seconds of measured traffic; the later gradual500ramp never started. Network, hardware, cache and scheduling differences remain possible contributors; their numerical shares are not established.

## 503s are not hidden

Original sessions.js records a structured429/503 with positive Retry-After in controlled_overload and rejection_reasons. It is excluded from successful_latency, not silently counted as a success. Every normal staged qualification requires controlled_overload.rate==0, checked both by k6 and independent stage_gate.mjs. Any such rejection therefore fails the staged capacity criterion. Nonconforming503s count as unexpected failures. The separate burst profile's permissive overload rule is NOT the staged20/100/ramp500/sustained500 policy.

The overload HTTP path was exercised with a real handler in regression, before database access, plus backlog/cancellation/cross-controller retry tests. It was not activated in the sustained mixed replay (which recordedzerooverload). This is a deliberate distinction, not a claim of overload stress qualification.

## Mobile retry correction, separate from frozen backend

Inspection found no automatic POST retry loop. ProviderConversation retains the same client message UUID for an unchanged failed draft; Town Hall offer/create references similarly remain tied to their payload fingerprints. However communityApi only parsed Retry-After for429, ignoring503.

Fixed that narrow client gap on local mobile branch fix/community-overload-cooldown, commit3448048: honor numeric Retry-After for429and503; retain bounded per-account/path mutation cooldowns; reject early manual POST attempts locally without fetch; allow the next explicit attempt after the server deadline. No background POST retry timer, new automatic retry loop or payload/reference substitution. The retained map is bounded64entries and expired entries are pruned. No Play Store/App Store binary was built, submitted or deployed.

Verification: test-community-api.cjs covers503metadata, early retry blocked before network, the1999ms boundary, acceptance at2000ms, unchanged body/client_message_id and no leftover timers. Existing chat composer and product chat/options tests pass, including lost-response idempotency. TypeScript noEmit check emitted no errors. Legacy product/native-provider transports do not receive the new shared-room queue503 because that queue guards shared chat Send and HallOffer; this is not a claim that every mobile transport now implements a common retry scheduler.

## Telemetry limit and next decision

Current VPS runner retains pool acquisition counts/cumulative wait, active/max connections, sampled database lock waiters, pg_stat_statements SQL mean/max timings, container CPU/memory and cgroup throttling. It does NOT provide per-request pool-wait/lock-wait percentiles. The public mixed replay likewise does not supply those percentiles. Do not relabel sampled waiter counts or averages as p95.

Obtaining true per-request percentiles would require additional targeted instrumentation and validation. That is a choice for the next execution review, not a reason to claim instrumentation already exists or silently replace the frozen binary. The next real capacity decision must come from one explicitly authorized isolated VPS checkpoint using the tested binary, original gates/caps/sequence, complete evidence and cleanup. The reviewer's recommendation is not user authorization. No new live command or automatic retry was issued.
