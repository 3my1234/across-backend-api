# Across Backend Deployment

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