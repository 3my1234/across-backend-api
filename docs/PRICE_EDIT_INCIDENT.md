# Price edit and admin refresh incident, 2026-10-05

The supplied log shows provider saves of the Smart Watch at 16:52:03 and
16:52:56 (backend log timezone), and approval at 16:54:34. Those requests
succeeded. Admin activity was repeatedly fetched, but the merchant-product list
was not fetched between the edits and the admin's later login. Product updates
do not necessarily create an activity event, so activity-only refresh missed them.

A fresh public API detail response contained `catalog_version=7`, that latest
approval timestamp, and the NG destination offer's item price of NGN 20. This
establishes that the buyer API was delivering the stored destination price,
rather than an earlier product snapshot. The live catalogue revision endpoint
returned 200, establishing that migration 057's revision table was available.

Remediation:

- Editing the provider main-price field now links matching-currency destinations
  automatically unless the provider explicitly elects to retain custom prices.
- Saving a different same-currency destination item price requires explicit
  confirmation in the provider form and API payload. Missing confirmation is a
  validation error, including for older browser code. Delivery fees and foreign
  prices remain separate.
- Admin moderation shows the actual destination item and delivery prices. A
  mismatch requires an explicit acknowledgement before approval.
- Admin and provider pages share a foreground catalogue revision watcher,
  refreshing relevant lists on changes and foreground return. It does not
  re-fetch billing or reset open provider edit forms. Busy moderation defers a
  change without consuming it. Stale admin responses cannot overwrite fresh ones.

This release requires no new database migration or mobile code change. The
existing watch offer still requires one provider save with the intended main
price/link followed by approval; no live database credentials are configured
here, and this work must not be reported as having rewritten that record.

Deployment acceptance:

1. Confirm deployed portal assets use `20261005-price-guard` and load
   `catalog-live.js`. Refresh each browser once to pick up the new application
   code (logout is not required).
2. Edit the Smart Watch's main price with custom-price confirmation unchecked.
   Matching-currency rows should become `primary` and retain delivery charges.
3. Save. The pending edit should appear in the open admin Providers view within
   five seconds plus network time, without logging out.
4. Confirm admin's Buyer NG item price matches the desired main price, then
   approve. Check the public detail, list, flash sale if applicable, mobile detail
   and cart. Verify the actual price values, not just successful status codes.
5. Explicitly confirmed custom prices must still be allowed. An older client
   sending a mismatch without confirmation must receive a clear validation error.

Log errors in this excerpt: 404 for `/robots.txt` and `/sitemap.xml` crawler
requests, and 401 for one invalid admin login. Root 200 requests are health checks,
OPTIONS 204 requests are successful browser preflights, and `error=-` means no
request error. None of those lines records a failed product save or approval.
