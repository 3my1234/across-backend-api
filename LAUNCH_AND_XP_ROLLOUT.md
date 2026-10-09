# Launch waitlist and XP rollout

Deploy backend before portal, website and APK. Automatic migrations run with RUN_MIGRATIONS=true. Manual fallback in the newly deployed container:

```sh
cd /app && ./across-migrate
```

066 adds the approved Basic/Growth/Business tiers; Basic retains its existing Flutterwave card agreement. New tiers accept bank transfer while their recurring card plans are unconfigured. Existing active plans require a support/admin change. Portal descriptions reflect separately enforced product and service/property capacities.

067 switches new checkout quotes away from XP discounts and adds reviewed cash withdrawal requests (1 XP = NGN 1; minimum 1,000, existing XP included). New signup: 5 XP. Daily login/review: 1. Purchase points only after completion, capped by 10% of retained fees and 25 per order. Pending legacy checkout XP is still reserved, and old payment attempts can reconcile. Test a repeated withdrawal submission, reject and verify one refund, then test processing/paid with a unique reference. Paying is a manual bank operation; the dashboard only records it. Review identity, bank details, reward history and budget before paying.

068 adds the public waitlist and private super-admin list/export. Check main-domain CORS, submit a real optional-fields-empty signup, verify Admin > Launch waitlist and CSV, then repeat and confirm one contact. The public API never lists contacts. Confirm no launch emails are sent automatically. Handle support-email removal requests before any mailing; collected addresses are not ownership-verified.

Deploy portal and website next. Ads can link to https://atlxpres.com/?utm_source=tiktok#waitlist, substituting twitter/facebook as needed. Provider plans start at NGN 500/month; avoid unverified cheapest/most-interactive claims.

Install a new APK for feed loading and wallet UI. Test Services/Products first paint, changing filters on a slow connection, failed refresh retaining data, imported products, old APK checkout without discounts, cash-wallet history, bank form and account switching. Check physical notification sound separately; this change does not prove handset delivery/sound.
