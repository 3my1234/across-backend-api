# Contact-only products and permanent provider tiers

Products now use in-app seller messaging. New quote, Flutterwave product checkout,
saved-card charge and settlement-account configuration endpoints reject requests,
including old clients. Historical orders, transaction verification, tracking,
settlements and reconciliation remain available. No historical ledger is deleted.

Free replaces the temporary free-launch period. Document approval is required to
publish; Free allows one nonarchived listing total across every category. Existing
listings are grandfathered: this change does not delete or unpublish them. Providers
above their current capacity cannot create additional listings. Paid limits retain
the existing per-category policy:

| Stable code | Monthly NGN | Badge | Product limit | Service/property limit |
| --- | ---: | --- | ---: | ---: |
| provider-free (automatic entitlement) | 0 | Neutral verified | One combined listing | Shared with products |
| provider-monthly | 500 | Bronze | 5 | 5 |
| provider-growth | 1000 | Silver | 15 | 15 |
| provider-business | 2000 | Gold | 40 | 40 |
| provider-diamond | 5000 | Diamond | 80 | 80 |

A coloured badge requires approved active verification, an active subscription
within its paid period, and a confirmed positive NGN subscription payment. Colours
identify subscription tiers, not service quality or guarantees. Expiry removes the
paid style and ranking; approved providers retain neutral verification. Pending,
rejected or suspended providers have no verified checkmark and cannot publish.
No new cancellation, refund, grace-period or immediate upgrade rules are introduced.
The existing portal prevents duplicate active subscriptions; plan changes still
follow existing support/billing procedures.

Paid approved listings precede Free listings in public discovery. Rank-aware cursors
keep newer Free listings reachable after older paid listings. Existing relevance,
location and rating filters remain; no priority difference among paid tiers is
introduced. Cards show compact public avatars, names and badges; paid mobile cards
show Promoted. Private verification documents are never used as avatars. Badges are
SVG vectors; lists are static and detail/profile shimmer respects reduced motion.

## Deploy safely

1. Take a successful current-primary backup. Keep API and worker connected to the
   dedicated DB through 10.77.0.2:5432. Never restore over the active primary for
   this release.
2. Apply 077_provider_identity_contact_products.sql,
   078_diamond_provider_plan.sql and 079_permanent_free_provider_tier.sql using
   the new backend image's `/app/across-migrate` against MIGRATION_DATABASE_URL.
   These migrations must finish before the new API handles requests. If deploying
   through the existing API startup path, set RUN_MIGRATIONS=true for this API
   deployment; it applies pending migrations before starting HTTP. Keep it false
   on the worker. Restore the API flag to false after successful deployment if
   migrations are managed as a separate release job.
3. Redeploy API, then worker from the same revision. Check `/api/v1/ready` on both.
   Deploy admin and provider portal; the provider resource uses Dockerfile.provider.
   It has its own deployment trigger even though it shares the admin Git repo.
4. Build/install a new mobile APK (and production store bundles) for the changed
   UI. The backend blocks product payments from older APKs too. Existing installed
   provider PWAs receive the updated shell through the normal update prompt.
5. Diamond bank-transfer checkout needs no recurring plan ID. For Diamond card
   renewal, create an active Flutterwave NGN 5000 monthly plan, then link its ID to
   the existing provider-diamond row using the admin link/price control. Do not
   enter a Flutterwave numeric ID into the internal Code field or recreate plans.
6. Smoke-test Free product OR service creation, rejection of a second combined
   listing, document moderation, seller chat/images, paid subscription confirmation,
   coloured badge, paid-first browsing, support push and historical order tracking.

Automated coverage includes real migration-based verification/expiry/renewal and
quota concurrency tests, all four badge tiers, Diamond migration idempotence,
retired payment endpoints, paid-first paging, escaped portal identities, PWA caching,
mobile TypeScript and Android release JS export. Linux CI also builds API/worker
and the isolated provider image. A successful JS export is not an iOS device test
or proof of live Flutterwave collection; those require release smoke tests.

Friends, verified-phone contact discovery, invitations, user statuses and
connection-based provider ads are explicitly deferred to the next phase. No native
contact permission, address-book upload or friends-of-friends targeting is included
in this release.
