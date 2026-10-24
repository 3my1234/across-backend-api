# Provider subscription billing

Implemented in the integrated candidate; production activation is not authorized.
The existing direct APK uses Flutterwave. The Play build uses native Expo IAP,
with monthly renewal and configured 1/3/6/12-month prepaid products. Existing
Flutterwave subscribers retain cancellation management inside the Play app.

The authenticated owner obtains server-owned product mappings and reserves a
15-minute checkout under the existing provider-renewal lock. Google callbacks
and restores submit only a token and product identifiers. The backend verifies
provider/account binding, subscription state, exact product/base plan and expiry,
then Google's processed order, token ownership, listing price and actual NGN paid
amount. Receipt/token uniqueness, atomic entitlement/payment records and exact
Google expiry prevent duplicate periods. Acknowledgement follows durable commit;
its uncertainty is retried, not presented as a second payment. Test purchases
cannot create financial receipts or the additional495 referral XP. Real provider
referrals remain500 total per referred person, once.

Pending purchases are retained without paid receipts/access/XP. Holds, expiry
and canceled pending payments remove entitlement; canceled paid subscriptions
retain access to Google's confirmed expiry. Terminal tokens stop polling. A
verified, newly purchased duplicate that cannot be fulfilled is placed in a
durable full-refund queue; no client cancellation invokes a refund. A refund is
labelled confirmed only after Google's successful response. Existing access is
preserved. Reconciliation is bounded and uses the existing worker/database.

Authenticated Google Pub/Sub notifications are being qualified in this branch.
They validate Google signatures, audience, issuer and the dedicated sender email,
then the exact subscription/package. Message IDs, hashes and authenticated-encrypted tokens are queued; no plaintext token is retained.
Events never grant access directly. An existing business-account hash is registered
before checkout. If the app closes before verification, the worker obtains the
account binding from Google, resolves that same business and runs the complete
receipt/order transaction. The worker always rereads current Google state. Duplicate/out-of-order events cannot extend
periods. Reconciliation claims up to50 records with four-way concurrency, prioritizes
notification work, and uses hourly fallback checks when notifications are configured
(five minutes otherwise), with additional checks near expiry. Aggregate due,
acknowledgement, refund and event backlogs appear in the existing private ops API.
Actual public delivery and store transactions are NOT yet accepted.

## Server configuration and migration approval

Migration104 is additive and not applied to production. Existing Flutterwave IDs
are preserved; Google order IDs have their own receipt field. Product mappings
start empty/disabled. Purchased mappings are immutable; retire an old mapping and
add a new one for price changes. Owners can still restore retired products.

Optional server-only configuration (never EXPO_PUBLIC):

- PLAY_BILLING_ENABLED=true only after separate activation approval.
- PLAY_BILLING_SERVICE_ACCOUNT_FILE: protected mounted Publisher credential.
- PLAY_BILLING_TOKEN_KEY: separate random base64 32-byte AES-GCM key, identical
  across API/worker/replicas and retained securely for recovery. Do not rotate JWT
  keys to configure billing or expose either key in logs/chat.
- PLAY_BILLING_ALLOW_TEST=true is allowed only in explicit test/staging/development.
  Production rejects it. License-test access is not evidence of a real payment.
- Optional PLAY_RTDN_AUDIENCE=https://api.atlxpres.com/api/v1/webhooks/google-play,
  PLAY_RTDN_SERVICE_ACCOUNT_EMAIL and PLAY_RTDN_SUBSCRIPTION must all match the
  authenticated Pub/Sub push configuration. Creating/configuring cloud resources
  and potential usage charges requires separate approval; none was provisioned.

Before activation verify Publisher purchase/order/acknowledgement/refund access,
real product IDs/base plans, NGN prices and prepaid/renewal terms. Seed mappings
only after review. Verify a licensed test purchase, pending recovery, restore,
cancellation/expiry, notification delivery and duplicate refund in isolation.
Enable the play_billing worker role (included in default all) on a compatible
worker and verify its progress before enabling public purchases.

## Release and rollback

Keep PLAY_BILLING_ENABLED absent/false for the foundation/schema deployment until
store setup and acceptance are complete. Take a fresh S3 backup and retain current
images/settings. Execute the exact-source migration toolkit before new consumers;
the old Coolify pre-hook is not a candidate-schema gate. Keep migrations additive.

The existing ec60b2a rollback drill qualifies the pre-activation application path.
Once Google purchases exist, rollback must retain a billing-aware API/worker and
migration104; old binaries alone cannot reconcile Google entitlement/refund work.
Disable new purchases if needed, preserve token encryption keys/ledger/queues and
continue compatible reconciliation. Do not roll back financial records or delete
migration104. A production cutover/recovery remains an explicit approval gate.

Automated synthetic Google transports and disposable PostgreSQL fixtures are not
real Play purchase, public webhook, real-device or production readiness evidence.
