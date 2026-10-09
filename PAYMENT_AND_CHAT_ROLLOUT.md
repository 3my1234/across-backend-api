# Product payment choices and seller chat

Each product has one payment mode: `flutterwave`, `contact`, or `both`.
Existing products and older create requests default to Flutterwave. Older
edit requests that omit the mode preserve the saved choice. Contact-only
products remain eligible for publication without a seller payout account;
business verification, subscription access and moderation still apply.
The server rejects contact-only products in new checkout quotes. Existing
payment attempts can still be verified and recovered.

Product conversations use the existing provider inbox. Buyers find them in
Services > Provider chat or reopen them from product details. Providers reply
in Messages. Directly arranged payments are outside app checkout and do not
create paid orders or tracking entries.

Each message supports four JPG/PNG/WebP images, at most 5 MB each. Uploads
are scoped to the sender, checked against storage before saving, and returned
as 15-minute signed URLs only to conversation participants. The public image
proxy rejects the chat prefix. Client message references prevent duplicated
messages after an uncertain response; changing their content is rejected.

## Rollout

1. Deploy the backend and apply migration **064** before serving the new API.
   `RUN_MIGRATIONS=true` applies it at startup. If disabled, run
   `cd /app && ./across-migrate` in the new container.
2. Keep S3 private. The backend IAM key requires `s3:PutObject` and
   `s3:GetObject` on
   `arn:aws:s3:::<your-bucket>/user-uploads/private-chat/*`.
   GetObject also authorizes the HEAD checks. Preserve existing provider
   upload permissions and CORS for the provider portal's PUT requests.
3. Deploy the provider portal, then build/install a new mobile APK. An APK
   already built or queued before these commits does not contain product
   messaging, attachments or the new payment-choice controls.
4. In Flutterwave Business preference settings, turn **Disable preferred
   payment methods** off so the API's `payment_options` is respected. Confirm
   bank transfer is available for the Nigerian merchant checkout.
5. Perform a small real bank-transfer checkout for a product and a provider
   plan. Check gateway success, the application's verified payment history,
   order tracking/cart clearing, and one-month provider activation. Automated
   tests use a fake gateway; they do not establish live merchant enablement.
6. Test all three product choices and a buyer screenshot/provider photo reply.
   Reopen both inboxes and confirm images and history load. Confirm unrelated
   accounts and the public image proxy cannot access chat photos.

## Transfers and settlement

Nigerian product checkout uses its existing country policy, including card
and `banktransfer`. Provider subscriptions now offer recurring card payment
or a separate bank-transfer payment for one month. Transfer checkout omits
the recurring Flutterwave payment-plan ID and requires manual renewal.
Verification uses the existing gateway verification and payment ledger.
Repeated callbacks do not grant extra months or revive an expired transfer
subscription; another transaction cannot reuse the same one-time reference.

Seller and buyer explanations distinguish successful payment from the seller's
bank payout. Flutterwave generally settles local payments the next business
day and international payments after five business days; delays can occur.
Its split-payment cycle does not wait for buyer delivery approval.

Sources: [Flutterwave Standard](https://developer.flutterwave.com/docs/flutterwave-standard-1),
[Flutterwave settlement FAQ](https://www.flutterwave.com/gb/support/payments/settlement-frequently-asked-questions).

## Validation

Run `SETTLEMENT_TEST_LOCAL=true go test ./...` against a loopback PostgreSQL
database and `go vet ./...`. Integration coverage includes the real migration,
seller create/edit/submit routes, catalogue visibility, blocked checkout,
conversation permissions, photo-only messages and retry deduplication, and
card/transfer checkout payloads with verified entitlement and replay handling.
Mobile and portal scripts additionally cover photo upload, retained retries,
session isolation, expiry refresh and acknowledgement handling.

## Appearance and prepaid-period rollout (9 October 2026)

The latest migration is **065_prepaid_provider_subscription_periods.sql**.
Redeploy the new backend image first. In Coolify, `RUN_MIGRATIONS=true`
(the default) applies all pending migrations before the API starts. The
runner holds a PostgreSQL advisory lock, uses the locked connection, and
commits each migration's schema changes and applied record atomically.
It stops startup on a migration error. Re-running skips recorded migrations.

If startup migrations are disabled, run `cd /app && ./across-migrate` in
the NEW backend container. Take a database backup before production schema
changes. Do not edit applied migration files. For a larger deployment,
run one migration job before rolling out API replicas and keep schema
changes compatible with the old version during that rollout.

Deploy the portal after migration 065. Transfers support 1, 3, 6 or 12
months at the existing monthly rate. Cards remain monthly recurring.
The server calculates totals, saves the purchased period/amount, and
checks verified payments against that purchase even if the plan price
later changes. Duplicate callbacks do not extend prepaid access.
Older portal requests default to one month. Older backends do not advertise
longer periods, so the new portal exposes only one month until deployed.

Appearance is System/Light/Dark, saved locally. Portal controls appear in
the header (and admin sign-in); mobile controls are Account > Appearance.
The mobile change requires a NEW build from the updated source. A queued
build from an earlier commit does not acquire these changes.

Before real charges, test transfer/card payment, plan activation and expiry,
product payment history, tracking/cart clearing, XP redemption, seller
messages/photos, and both light/dark themes. Use the exact amount and
account Flutterwave displays. This release's gateway tests are simulated;
no real charge or production migration was performed during development.
