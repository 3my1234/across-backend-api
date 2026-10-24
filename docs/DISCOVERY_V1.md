# ATL Xpress discovery and subscription promotions

This is an independent release from the one-account correction in PR72. No production migration, deployment, EAS submission or paid infrastructure is authorized by preparing this branch.

## Existing implementation examined

- `catalog_controller.go`: paid catalogue priority and category-related `/products/:id/recommendations`; existing products/stock/delivery/price rules.
- `provider_marketplace_controller.go`: paid-first service feed, category/exclusion filters and existing detail/contact/chat routes.
- `provider_public_identity` (migrations077/079): verification, active business, settled NGN subscription payment, current period and listing limits.
- `community_promotions_controller.go`: older consent/friend-path promotion, not a general paid-listing discovery pool. No friend graph or private message contents are reused.
- Native `Screens.tsx` and `MarketplaceScreen.tsx`: existing related product/service sections mislabeled Recommended. The provider PWA has business management pages, not a customer shopping/detail application; its appropriate companion is promotion controls/reporting, not ads inside business forms.

## Implemented first version

`GET /api/v1/discovery` returns separate `similar` (when a source is provided) or `recommended`, plus `promoted` cards. Products and services share public cards but open their original detail routes and existing contact/chat flows. Similarity uses category, public title/description keywords, current search and country/city. Useful cross-kind matches share those signals (for example phones and phone repair). It is not semantic understanding of all complementary purchases.

Recommendations use current context and a bounded recent-topic history held only in app memory for the current account/session. Anonymous/new users receive country-eligible, nearby/rotated candidates. No message contents, permanent behavioral profile, cross-device identity graph, machine-learning vendor or new database service is introduced. Saves and inferred purchases are not signals in this version.

Public eligibility is checked live: approved, published, available, within the current plan's visible listing slots, active/verified provider, supported destination/market; products also need stock and an available delivery offer. All paid providers' eligible listings enter promotion automatically unless their provider or listing preference opts out. Organic alternatives can include eligible Free listings. Expired subscriptions lose promotion eligibility while permitted organic listings remain.

Relevance precedes promotional weight. Among equally relevant candidates, deterministic weighted rotation uses Bronze1/Silver2/Gold3/Diamond4 and the provider's optional listing preference1–3. These are relative opportunities, not guaranteed impressions or delivery ratios. Every rail has at most one card per provider; cards are deduplicated across rails and never include the source. Up to three promoted slots are reserved before up to six organic cards; recent actually visible listings get a small recency penalty when alternatives exist. Client session IDs are not billing identities and can reset between app sessions.

The mobile UI labels Similar Listings, Recommended for You and Promoted separately, shows static image cards and opens original details for the complete image/video experience. It does not autoplay video ads. Placement is limited to home/search and details, never private conversations or checkout. Loading, retry and sparse/empty states are explicit. Existing actual prices and red crossed prices are preserved on the new cards.

Provider mobile More and provider PWA expose opt-out, per-listing priority and 30-day promoted results through authenticated owner-scoped routes. Only owner/manager can change preferences. Current listing controls are bounded to the latest50 records (standard plans currently permit at most40 combined listings); expanding custom plans beyond50 needs paginated controls.

## Measurement and privacy

Serving a feed counts nothing. The native UI measures at least50% of the card in screen bounds continuously for1000ms while active, excluding covered detail/chat/workspace surfaces. Events are batched, rate-limited, size-bounded and require a two-hour HMAC receipt issued by the API. Receipt signatures use the existing signing secret with a discovery-specific prefix; no extra secret is required. Per session/listing/placement/event/UTC-day deduplication and counter updates are atomic in PostgreSQL.

Metrics are impressions, clicks, successfully opened details and contact-button taps. A contact tap does **not** establish that a chat was created, a customer purchased, or the provider earned revenue. Client viewability/signatures are abuse controls, not proof of human attention. Own testing can be counted; offline/blocked events can be lost. No advertising charge or payout depends on these measures.

`discovery_daily_metrics` holds aggregates only, with no user ID or message/search text. Dedup keys are hashed and expire after26h; aggregates retain90days. The existing worker removes bounded chunks once per minute. Monitor retention backlog at scale. Providers see only their own30-day promoted totals. The admin route exposes30-day placement aggregates, not individual browsing histories.

## Performance and operational limits

Four UUID seek windows cap candidates at128 each before presentation: at most256 product and256 service candidates, six organic and three promoted cards. Database request timeout is two seconds; no OFFSET, full-catalogue transfer or per-card API/subscription queries. Additive migration103 adds partial category/text/location indexes and owner preferences/metric tables. Feed responses are private/no-store; eligibility is never cached into a stale authorization grant. Redis already supplies shared request limiting; candidate caching is deliberately deferred until measurements justify an ID-only cache plus live revalidation.

This does not establish capacity for10k/50k concurrent users or millions of accounts. CI qualifies correctness on synthetic data and records the isolated query execution time. Country/category selectivity, sparse-catalogue fallback and the UUID candidate window can affect recall; measured growth may justify a maintained candidate index, caching or background ranking. Never bypass current eligibility with a cached payload.

## Release and acceptance

1. Review exact-source backend/mobile/PWA CI. Keep discovery out of the already-qualified account APK branch until desired.
2. With separate approval, apply migration103 once before starting a new discovery worker/API. Index creation can acquire locks; use the established migration/release gate and take a fresh backup.
3. Release compatible API, worker and replicas deliberately; check image alignment/readiness and public detail/eligibility. Old mobile clients keep their existing endpoints. No configuration, Redis TLS or proxy migration is required by this feature.
4. Release approved frontend changes; the user triggers EAS personally. Preserve the PWA resource until separately approved retirement.
5. On devices test both listing kinds, sparse/full pools, anonymous/new account, country/search context, all four tiers, expiry/suspension/stock/opt-out, correct detail/chat opening, account switching, covered/background cards and reporting.
6. Roll back frontend/API/worker to the recorded prior compatible images if needed. Leave the additive tables/indexes in place during rollback; do not drop business data or restore production as a routine code rollback.

Acceptance still required: physical screen/viewability and scroll behavior, user journeys and production-like staging performance. Long-term personalization, semantic complements, cross-device frequency controls, verified completed-chat/sale attribution and fraud-resistant billable advertising are later features, not claimed implemented here.
