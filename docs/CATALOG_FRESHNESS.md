# Product pricing and catalogue freshness

Delivery offers marked `uses_primary_price=true` use the product's primary item
price plus their delivery fee. Database triggers keep that relationship in the
same transaction as a primary-price edit. Independent destination prices and
prices in other currencies remain separate.

Apply migrations **056 and 057** with the deployed image:

```sh
cd /app && ./across-migrate
```

Migration 056 links existing offers only when their item price already matches
the primary price and currency. It does not guess whether an existing mismatch
is a mistake or a deliberate destination price. To repair an unintended mismatch,
edit the product in the provider portal, click **Use main price for NGN delivery
areas**, save, and approve the pending product again in the admin portal.

The API remains usable before these migrations run. During that window it stores
normalized offer totals without durable links, bypasses unversioned catalogue
caches, and `/api/v1/catalog/version` returns 503. Apply both migrations before
acceptance testing.

Migration 057 increments one durable catalogue revision on product, delivery,
service, provider eligibility, subscription, or market changes. Public response
cache keys include the primary database's committed revision and delivery
destination. Refresh query nonces are excluded from internal cache keys, so
buyers share a current response after revalidation. Clients and edge caches must
revalidate; explicit refresh responses use `no-store`. Operational catalogue
queries use the primary database to avoid replica lag. Internal cache entries
expire within five seconds, and the change token also changes each minute for
time-based subscription eligibility.

The mobile app shares one foreground version check every five seconds. A change
refreshes mounted lists and details, including flash sales, imported goods,
recommendations, services, and cart snapshots. Refresh and foreground resume
request fresh data immediately. Older responses cannot overwrite newer product
snapshots. Cart price/quantity changes invalidate an unpaid quote; active payment
confirmation is preserved. Offline services remain explicitly identified as saved
results.

Build 19 does not contain this mobile synchronization. Build a new preview APK
from the merged mobile main branch for acceptance testing. Do not use a new paid
purchase to diagnose catalogue display; verify the displayed price first.

Acceptance:

1. Repair the Smart Watch's main-price link and approve it. The public NG catalogue,
   product detail, and checkout item price must show NGN 100, plus any delivery fee.
2. Leave the new app open with that product in the cart. Edit a linked main price
   or description and approve it. Verify Home, detail, cart and recommendations
   update within five seconds plus network time. Pull to refresh without waiting.
3. Repeat with a flash-sale product and a service price, image, or location.
4. Background the app, approve another edit, and return. The data must revalidate
   immediately without relaunching.
5. Independently priced areas must retain their own prices; the main-price button
   must preserve their delivery fees and offers in other currencies.

Regression commands:

```sh
# Backend: use a loopback database and isolated test schemas.
SETTLEMENT_TEST_LOCAL=true go test ./...

# Mobile
npm run test:catalog
npm run typecheck
npm run lint

# Admin/provider static portal
node scripts/test-product-pricing.cjs
node --check provider.js
```
