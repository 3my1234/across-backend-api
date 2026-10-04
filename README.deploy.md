# Across Backend Deployment

For seller payout reconciliation, migration 052, and historical transaction
verification, follow [Seller settlements](docs/SELLER_SETTLEMENTS.md).

Provider subscriptions default to free launch access. Migration 053 adds the
super-admin **Provider access** switch in the Providers tab. It can set Free
or Paid and optionally schedule the start of paid access; the shared database
setting takes effect across API replicas within three seconds without a
redeploy. `PROVIDER_SUBSCRIPTIONS_ENFORCED` and
`PROVIDER_SUBSCRIPTIONS_START_AT` are fallback defaults until the first admin
save. Apply migration 053 before using the control. The API returns
`subscription.required` and `subscription.launch_access_active` to the provider
portal. Verification, listing moderation, and product payout-account
requirements still apply.

Before declaring a free launch, inspect existing Flutterwave payment-plan
subscriptions. Flutterwave can continue charging subscribers on an existing
recurring plan even when this app stops offering new checkout. Cancel or suspend
those gateway subscriptions separately, then verify their status. The free
access setting does not cancel gateway billing.

The admin plan price and linked Flutterwave payment-plan amount must match.
Flutterwave can use the app's amount for the first payment and the gateway
plan's amount for later recurring charges. Checkout and active admin plan saves
now verify the Flutterwave amount, currency, interval, and status and reject a
mismatch. The Providers tab's price control reuses the linked Flutterwave plan
when its amount matches, otherwise creates and links a new monthly plan for
future subscribers. Changing the advertised price does not reprice existing
gateway subscriptions. Migration 053 snapshots each checkout's agreed amount
so later plan edits cannot invalidate a delayed payment confirmation. The
**Existing Flutterwave renewals** table is a separate, explicit cancellation
control; using Free access does not cancel those renewals.

This backend is ready for Coolify as a Docker application.

## Coolify API Service

Use `Dockerfile` from the repository root.

Expose port:

```text
8080
```

Health check:

```text
/api/v1/health
```

Recommended production domain:

```text
https://api.atlxpres.com
```

## Required Environment Variables

Set these in Coolify, not in GitHub:

```text
APP_ENV=production
HTTP_ADDR=:8080
DATABASE_URL=postgres://USER:PASSWORD:HOST:5432/across_db?sslmode=disable
REDIS_URL=redis://default:PASSWORD@HOST:6379/0
REDIS_ADDR=HOST:6379
REDIS_PASSWORD=
REDIS_DB=0
REDIS_OPTIONAL=false
JWT_SECRET=replace-with-long-random-secret
FLUTTERWAVE_SECRET_KEY=replace-with-flutterwave-secret-key
FLUTTERWAVE_WEBHOOK_SECRET=replace-with-webhook-secret
PRIVY_APP_ID=replace-with-privy-app-id
PRIVY_APP_SECRET=replace-with-privy-app-secret
# Optional fallback; normally fetched automatically
PRIVY_VERIFICATION_KEY=
DEFAULT_COUNTRY=NG
```

## Transactional email deliverability

Set `SMTP_FROM_EMAIL` to an address on a domain you control and verify with the SMTP provider. Publish that provider's SPF and DKIM records in DNS, then add a DMARC record. The visible From domain should align with the authenticated SPF/DKIM domain; application HTML alone cannot prevent spam placement.

Recommended runtime variables:

```env
PUBLIC_BASE_URL=https://api.atlxpres.com
WEBSITE_URL=https://atlxpres.com
ASSETS_CDN_BASE=https://media.atlxpres.com
BRAND_LOGO_URL=https://api.atlxpres.com/api/v1/public/brand/logo.png
SMTP_FROM_NAME=Atlantic Express
SMTP_FROM_EMAIL=welcome@atlxpres.com
SMTP_REPLY_TO=support@atlxpres.com
```

`BRAND_LOGO_URL` must be a publicly reachable HTTPS image because email clients cannot load assets bundled inside the mobile application.
