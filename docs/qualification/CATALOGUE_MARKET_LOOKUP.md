# Catalogue destination-market lookup

The previous transaction breakdown identified an uncached catalogue cost independent
of Town Hall contention. The original query checked `countries_config` inside the
delivery-offer lateral lookup for every candidate product. The reviewed fixture
contains 5,000 products before the ordered 80-product limit.

The correction resolves the destination market's eligible currency with an
uncorrelated scalar subquery. Production schema enforces a unique country code.
The same active-market, Flutterwave, currency, destination-specificity, provider,
payment, subscription, location and stock predicates remain. Selection order,
80-product limit, response fields and existing ignored search/limit behavior
remain unchanged. This introduces no index, migration or cache TTL change.

## Initial matched SQL experiment

Loopback PostgreSQL 18 on Windows; the previously reviewed full synthetic snapshot.
Custom and generic plan modes, all/local/international stock scopes and with/without
coordinates: 12 cases. Each case compares complete ordered row values, then runs
original/candidate/candidate/original EXPLAIN ANALYZE arms.

All 12 ordered result comparisons passed. For the eight nonempty-result cases,
mean execution time across the two original/candidate arms improved 12.8–28.0%.
Empty international-result cases remained around 2–3 ms. This is SQL execution,
not end-to-end API latency or a 500-session capacity result.

A separate covering delivery-area index candidate preserved results but produced
inconsistent timings, including slower cases. It was removed from the disposable
database and is not part of the correction.

Raw evidence is retained under `qualification/townhall-txn-local/`:
`catalogue-market-comparison.json`, `catalogue-market-comparison.log`,
`catalogue-market-candidate.sql`, `catalogue_market_compare.go`, and the rejected
`catalogue-cover-comparison.json`. The original SQL is retained alongside them.

## Required verification

`TestCatalogMarketEligibilityAndDeliverySpecificity` checks national, state and
city offers; mismatched currency fallback; missing/inactive market; disabled
gateway; and market-currency mismatch. Existing ignored-parameter full-response,
pricing/delivery and cache-isolation regressions remain enabled.

The request-path comparison uses the existing full-schema transaction profiler,
with hot and independent rooms, real notification triggers and eight pooled
connections. Authentication and cache middleware are bypassed deliberately, so
these are cache-miss handler diagnostics, not full application qualification.
Town Hall locking and notification transaction boundaries are unchanged.

## Completed verification, 2026-11-06

Runtime correction: `395ef241b9a35be9712cb6fc7d107bfe8be85f38`.
PostgreSQL 16 CI [42203503855](https://github.com/3my1234/across-backend-api/actions/runs/42203503855)
passed, including the new market test, existing pricing and ignored-parameter
response tests, native privacy/ownership tests, cache race tests and compilation.
All three catalogue controller regressions also passed locally on PostgreSQL 18.
The retained candidate plan executes the countries_config scan once (previously
5,000 loops), with 139.496 ms execution and 74.121 ms planning in that standalone
sample. Do not compare a single EXPLAIN sample with handler p95.

Matched full-handler replay: baseline `b41795e` followed by candidate `395ef24`;
both include the previous cache-key fix, use the same restored fixture, pool of
eight and unchanged workload. Each replay runs catalogue-only for 30 seconds,
then hot/independent/independent/hot traffic for 60 seconds per arm. The four mixed
arms have 20 clients: ten writers with five-second pacing and ten catalogue readers
with one-second pacing. The catalogue-only arm has ten readers.

| Arm | Catalogue handler p95 before → after | Offer lock-held p95 before → after |
| --- | --- | --- |
| Catalogue only | 1,015.9 → 789.1 ms | — |
| Hot 1 | 789.2 → 659.6 ms | 316.8 → 210.7 ms |
| Independent 1 | 810.4 → 629.6 ms | 149.3 → 101.5 ms |
| Independent 2 | 821.6 → 567.8 ms | 169.8 → 59.3 ms |
| Hot 2 | 872.9 → 643.0 ms | 178.9 → 76.6 ms |

Catalogue handler p95 fell 16.4–30.9% across the five arms. Hot-room offer lock
statement p95 was 717.7 → 401.6 ms and 332.5 → 162.5 ms. Offer pool acquisition
was mixed: 432.6 → 469.5 ms in the first hot arm, 369.7 → 103.9 ms in the second.
Do not claim that pool pressure is eliminated, or attribute every timing change
causally to this single before/after replay. These local measurements show
variation and are not randomized hardware-isolated capacity qualification.

Both replay tests passed their response/status/accounting assertions: 3,155
baseline and 3,176 candidate requests. This is not an independent per-key write
reconciliation or a full authenticated k6 qualification. Neither middleware nor
full production request-path latency was measured here. No room lock, eligibility
check, transactional notification, connection-pool limit or acceptance threshold
was changed to produce these results.

**The uncached catalogue still exceeds the 500 ms target. 500-session VPS capacity
remains unqualified.** There is no justification to declare readiness or repeat
the old VPS package. Further work must address the remaining catalogue work and
pool pressure before a new capacity checkpoint; the current qualification package
still pins the older API and contains neither catalogue correction.

Evidence: `qualification/townhall-txn-batch-before/` (baseline name is historical;
no batching candidate was implemented), `qualification/townhall-txn-market-after/`,
`qualification/catalogue-market-handler-comparison.json`, CI logs and
`qualification/catalogue-market-evidence-sha256.json`. Both profiles completed in
about 275 seconds each. The rejected test index is absent. The owned loopback
PostgreSQL instance was stopped; the pre-existing Windows PostgreSQL service
remains running. No VPS access, setup, load, production change or purchase occurred.
