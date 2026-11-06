# Completed transaction-level diagnosis

2026-11-06. This completes the requested lock/pool/commit/response breakdown,
using real handlers and the exact reviewed full synthetic snapshot. No VPS
connection, deployment, production schema change or new VPS test occurred.

## Reproduction and limits

- New PostgreSQL18 instance bound only to127.0.0.1:55439; separate from the existing
  Windows PostgreSQL service. Restored snapshot SHA256
  58bfeeb9ddf6bd0229fa0f39bb02e71d5f472d2f64b9e3fd6bbea210eb00b802.
- Full500actorpairs,100050rooms,1025000sharedmessages and500000marketplace
  messages; actual schema, indexes and notification triggers restored.
- Actual CommunityController HallOffer/Send and CatalogController ListProducts
  over loopback HTTP. Test-only actor headers bypass authentication; this profiles
  handlers, not the full authentication/middleware/session stack.
- One API process/pool capped at8connections. VPS qualification has two API
  replicas with8connections each. PostgreSQL18/Windows has different hardware,
  planner and I/O behavior from PostgreSQL16/Linux on Contabo. No CPU quota or
  capacity equivalence is claimed.
- Redis public caching is deliberately absent from this handler-level profile.
  Catalogue measurements describe the **cache-miss handler path**, not normal
  cached API response latency. Production has revision-aware shared caching.
- Catalogue-only control:10readers for30seconds. Matched arms:10writers plus
  10catalogue readers, hot/independent/independent/hot,60seconds per arm. Writers
  alternate offers/messages every5seconds; readers request once per second.
  Independent writers use their own rooms; hot writers share their fixture's
  busy room. Hot rooms also have more notification recipients, so distribution
  changes both serialization and legitimate fanout work.
- All3135requests in the completed run returned their expected200/201/204status.
  This is response accounting, not a new independent exactly-once reconciliation
  qualification. Existing correctness requirements/tests remain necessary.

## Measured p95, milliseconds

Ranges show the two corresponding arms; they are not confidence intervals.
Do not add separate percentiles to construct a hypothetical request.

| Offer measurement | Hot room | Independent rooms |
| --- | ---: | ---: |
| Handler duration | 992–1240 | 189–417 |
| Pool acquisition, cumulative per request | 493–548 | 120–168 |
| Room-lock statement including waiting | 326–562 | 3.13–3.18 |
| Lock held through commit | 179–243 | 109–132 |
| Commit | 22–35 | 35–36 |
| Handler work after commit | <1 | <1 |
| Notification batch including actual triggers | 43–71 | 4–17 |

Lock observations independently sampled pg_stat_activity and pg_blocking_pids
every250ms:1140samples,98lock-wait observations, with tuple and transactionid
waits. The lock-statement duration includes query execution and scheduling;
it is not falsely labeled as an exact isolated lock-wait timer.

The hot-room send path varies more: p951060ms in the first hot arm and102ms
in the last. Both first5seconds and later samples are retained. This was a warmed,
analyzed fixture after instrumentation development; first5seconds must not be
described as a pristine cold-cache test.

Catalogue-only cache-miss handler p95 was1186ms, main SQL plus row consumption
p95826ms, provider-identity query p9543ms, pool acquisition p9595ms. Per-request
handler time outside measured SQL/acquisition had p9518ms. Catalogue remained
slow without Town Hall writers. In mixed arms its p95 ranged687–1175ms.

Standalone catalogue EXPLAIN ANALYZE after the concurrent experiment measured
179ms custom/202ms generic execution, with approximately61/66ms planning.
The plan evaluates delivery offers5000times before returning80products; the
cache-miss path has significant intrinsic SQL work as well as load-dependent
queuing. This is not a claim that the same milliseconds apply on Contabo.

## Decisions

1. Keep room locks and existing admission/closure/removal/idempotency guarantees.
   Distributed-room requests are much faster; there is no evidence supporting
   removing the correctness boundary.
2. Do not move notifications outside the transaction without an atomic durable
   replacement. Their SQL/triggers contribute to lock hold, but commit flushing
   and post-commit response processing are not the dominant observed costs.
3. Target catalogue cache-miss SQL work alongside hot-room critical-section work.
   Increasing connection pools would not eliminate either cost.
4. No SQL or locking candidate was adopted. An identity-materialization catalogue
   candidate saved only a modest amount and changed the ordered selected rows
   in this fixture; it was rejected rather than changing the response to claim
   improvement. API SQL, filters, ordering and80-item behavior remain unchanged.
5. A next candidate must reduce measured work while preserving ordered results
   and transactional correctness, then pass a matched comparison and existing
   concurrent correctness regressions. No unchanged VPS retry is justified.

## Narrow catalogue cache correction

Following the measurements, commit b7e1a87 implements a separate seven-line
runtime correction in internal/middleware/scale.go. The exact `/api/v1/products`
endpoint ignores `search` and `limit` today, but its public cache formerly keyed
them as different responses. Equivalent requests could therefore each trigger
the same expensive cache-miss handler. Only these ignored parameters on that
exact route are normalized away. Service searches, other product routes and all
delivery/location/stock filters remain distinct. Revision, time bucket, encoding,
country-header isolation, TTL, authorization bypass and overload behavior remain
unchanged. No request action or qualification acceptance threshold changes.

Matched regression: the original middleware ran3handlerfills for30simultaneous
requests across3equivalent variants; corrected middleware runs1. This is a
reduction in duplicate handler loads, not a measured percentage improvement in
end-to-end latency. A real PostgreSQL handler test verifies the entire ordered
25-product response is identical with no parameters, limit1/nonmatching search,
and limit20/hotel search. Adding actual pagination/search in the future must
update the cache-key policy; that contract test will fail if semantics change.

Local cache/freshness/isolation tests and actual PostgreSQL18 controller tests
passed. PostgreSQL16 and race verification run42197490500 PASSED in1minute32seconds
at b7e1a87, including the actual response-equivalence regression, existing native
ownership/privacy tests, middleware race tests and handler/route compilation. This source
change has not been deployed or included in a new VPS qualification image.
The first cache miss still has the intrinsic query cost measured above; do not
claim this change alone fixes all hot-room or catalogue latency.

## Evidence and cleanup

Reproducer: internal/controllers/town_hall_transaction_profile_test.go.
Shared workspace qualification/townhall-txn-local contains SUMMARY.json,
transaction-breakdown.json, per-request requests-arm0..4.json, transaction-profile.log,
lock-watch.jsonl and catalogue plans/comparison. An earlier interrupted diagnostic
had zero-copy request-label aliasing; it is retained as incomplete-label-profile.log
and excluded from these results. The completed implementation clones labels and
asserts classification, request counts and expected HTTP statuses.

The owned diagnostic PostgreSQL instance was stopped successfully with pg_ctl
against its exact data directory. The pre-existing postgresql-x64-18 service
remains running. Evidence and the stopped fixture files are retained. All test
and observer processes ended. This is completed diagnosis, **not500-session VPS
qualification**, and no production optimization has been deployed.
