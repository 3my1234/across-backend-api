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
