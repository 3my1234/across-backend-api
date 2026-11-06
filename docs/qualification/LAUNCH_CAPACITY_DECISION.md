# Launch decision after catalogue corrections — 2026-11-06

## Decision

Stop making further SQL reductions the automatic next step. Freeze the current
candidate for a full request-path disposable checkpoint, retaining the existing
k6 workload, acceptance thresholds and correctness reconciliation. Production
deployment and another VPS test are not authorized by this document.

An uncached handler diagnostic above 500 ms is useful evidence, but is not by
itself a failed run of the actual cached application workload. Conversely, a
passing cache test cannot establish production capacity. The 500-session,
30-minute VPS qualification remains incomplete.

## Facts checked in source

- Qualification `deploy/session-lab/sessions.js` calls the catalogue without a
  token, although the session is authenticated for other operations. Public
  caching therefore is not bypassed by Authorization on these catalogue calls.
- Three search terms and the optional `stock_scope=local` produce six query URL
  variants. The exact-route normalization collapses these to two query families.
  These are not necessarily only two Redis keys: revision, encoding, country
  headers and other isolation inputs remain part of the key.
- The public cache has at most five seconds TTL, per-process fill coalescing and
  shared Redis storage. Cold misses, revision changes and different query
  families still incur backend work. Do not assume a hit rate without measuring.
- The unchanged qualification checks p95 below 500 ms and p99 below 1,500 ms
  overall and for each operation. It does not require every individual request
  or every isolated cold miss to finish within 500 ms.
- The pool constructor does not disable pgx statement caching. No evidence
  establishes that merely adding prepared statements fixes the remaining cost.
  Standalone EXPLAIN planning time must not be treated as per-request production
  planning cost without observing prepared-plan reuse.
- The retained candidate plan still evaluates delivery offers 5,000 times.
  Provider visibility/publication-limit evaluation is another remaining cost.
  Nested plan times overlap; they cannot be added as independent durations.

## Bounded next checkpoint

Use the current release source, including cache normalization and market lookup
correction, not the older API pinned by the VPS package. Verify source/binary
identity. Run the existing realistic full request path in disposable PostgreSQL
16/Redis infrastructure for several minutes at 20 sessions, retaining ordinary
room distribution and a deliberate hot-room component. Distinguish cache hits
and misses using existing response headers/log evidence; include their counts
and timings. Do not manufacture cache hits, remove normal cache expiry, bypass
authentication for protected operations, replace the fixture with a tiny data
set, or alter thresholds to pass. Preserve resource measurements, independent
per-key reconciliation, and cleanup.

If it passes, assess one separately approved VPS checkpoint with the same image.
If it fails, produce one consolidated go/no-go report identifying the failed
operation, hit/miss mix, pool/query/resource evidence and integrity results.
Do not automatically start another sequence of speculative SQL changes.

## Launch tracks

Store submission preparation and functional release verification can proceed in
parallel with capacity work. This performance evidence does not verify the
current Android/iOS binaries, payment/subscription lifecycle, account deletion,
privacy requirements, device notifications, backup restoration or deployment
rollback. Check existing release evidence for these before calling launch ready.

A limited initial rollout may be a business decision with explicitly accepted
unmeasured capacity risk, verified critical flows, recoverable deployment,
monitoring and an operator response plan. It must not be advertised as a passed
500-user qualification or assigned an invented safe concurrency number. Even
20 sustained VPS sessions are not yet qualified by the retained short failures.
An unrestricted 500-session launch has no supporting capacity evidence today.

No infrastructure purchase, K3s migration, production edit or automatic gate
relaxation is part of this decision.
