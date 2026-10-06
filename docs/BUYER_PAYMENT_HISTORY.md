# Buyer payment history and lost-checkout recovery

Account > Payment history lists the buyer's recorded product checkout attempts
and payments. The endpoint GET /api/v1/payments/history is authenticated, filters
both payment and order ownership, uses bounded timestamp/ID cursor pagination
(default 25, maximum 100; mobile requests 20), and returns private/no-store data.
Migration 062 adds the partial buyer/cursor index. No payment is initialized by
listing history. Saved reference, amount, gateway charged amount when available,
XP selection, date, confirmation/refund/dispute and seller payout are separate.

The history screen uses a virtualized list and revalidates on entry, foreground,
pull refresh and every 12 seconds while visible/active. Loaded older records are
retained, failures retain history and show retry, and stale/unmounted responses
are ignored. An exhausted older cursor stays exhausted after top-page refresh.

Check payment - no new charge sends the saved order/reference to the existing
verified-payment endpoint. It never initializes checkout. After verified success,
history refreshes and the order is available in Track. Matching saved checkout
state is cleared; unrelated carts are not cleared by recovery of another order.

Existing APK users can recover without this new screen using the super-admin
Transactions > Recover a confirmed Flutterwave payment form. Enter the order ID
and the full transaction reference from Flutterwave. Migration 061 must already
be applied for XP orders. Migration success alone does not verify payments, and
payment confirmation does not prove provider bank settlement.

Deploy the new backend, apply 062 using ./across-migrate, and build a new preview
APK for the Payment history screen. The already deployed 3af47e1 backend includes
061 and the admin verification endpoint, so immediate recovery need not wait.

Validation: full Go suite with isolated PostgreSQL integration; buyer ownership,
equal-timestamp paging, confirmation vs held payout, malformed cursors; mobile
typecheck/lint, actual loader and verifier regression scripts including failure
retention, saved reference verification and unmount protection. Physical-device
and production payment recovery remain live acceptance checks.
