# Play provider subscriptions — implementation checkpoint

The existing direct Android APK and physical-commerce Flutterwave paths stay
unchanged. The Play build must preserve provider purchasing through supported
store billing rather than silently disabling it or sending digital purchases to
the direct checkout. Existing provider identity, subscriptions and referral rules
remain authoritative.

Current implementation: internal/playbilling uses the official Go OAuth client
with a server-only service account, fixed Android Publisher scope/OAuth endpoint,
bounded cached refresh and cancellation. Receipt verification calls Google's
fixed TLS subscriptionsV2 endpoint for the existing package, binds obfuscated
account IDs to the authenticated provider, verifies exact product/base plan,
expiry/state/order and test eligibility, and prevents redirects/error-token leaks.
Pending/held/expired receipts never become active or eligible for acknowledgement.
No client price/expiry/account claim can grant access through this package.

This package is NOT yet wired to a production route or payment ledger. Durable
token/order deduplication, checkout intents, entitlement/reward transactions,
acknowledgement/refund handling, cancellations/renewals/reconciliation and native
purchase/restore UI are still required. Test credentials and mocked responses do
not prove a real purchase. No new SQL migration or production credential has been
created by this checkpoint. Do not deploy or claim full Play purchasing ready.

Native Expo IAP3.3.4 capability is prepared in mobile PR51; physical compile and
real Play acceptance are separate. Enrollment/product setup and service-account
access will be required later, but ordinary implementation continues now.

Sources: https://developer.android.com/google/play/billing/integrate and
https://developers.google.com/android-publisher/api-ref/rest/v3/purchases.subscriptionsv2 .
