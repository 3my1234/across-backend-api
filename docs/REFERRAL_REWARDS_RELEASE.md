# Referral rewards and display fixes

This candidate starts from deployed backend65a159b. It is not deployed. Mobile changes and migration102 must be released deliberately after approval; no automatic main rollout is requested.

## Policy

- A newly registered, verified customer or provider earns their referrer5 XP.
- A free provider stays at5 XP. The first server-confirmed NGN payment for Basic500, Growth1000, Business2000 or Diamond5000 adds495 XP, for500 XP TOTAL for that referred account.
- Monthly/prepaid/yearly payments use the same existing settlement ledger. Renewals, upgrades, callback retries and multiple subscriptions do not repeat the bonus.
- Plan choice, subscription status, browser checkout success and unverified registrations do not award XP.
- The existing signed xp_transactions ledger and1000-XP/admin-reviewed withdrawal process are reused. Welcome XP awarded to the new person is separate from the referrer's reward.
- This is once per attributed account. Email/phone uniqueness is not proof of a unique human. Manual payout review remains necessary for duplicate-account abuse and suspicious referral clusters. Cancellation does not reverse a legitimate first-payment referral reward. Chargeback/refund reconciliation is not automated by this change and must be reviewed before payout.
- Rewards consume real funds: the500-XP reward equals the face price of the500-naira plan's first month, before gateway fees. The policy is implemented as explicitly approved by the user, not inferred as a percentage commission.

## Attribution and interfaces

Codes are random12-character values, privately issued/read through GET/referrals/me. Public GET/referrals/validate returns only validity, without referrer identity. Optional referral_code is accepted only during new email registration and newly created, verified Privy identity registration. Attribution shares the creation transaction; invalid codes roll back creation. Existing logins cannot add or change a referrer, and self-attribution is rejected.

Database triggers reconcile verified signup and confirmed subscription-payment insertion. Referral-row locks serialize verification/payment races and concurrent renewals. Separate unique signup/provider XP event keys prevent duplicate credits and notifications. Stats expose aggregate counts, not other people's emails/messages.

Native customer Account and Provider workspace both expose Refer and earn XP. Registration and Google entry accept an optional code. The mobile client validates a nonempty code before platform account creation; an older/unavailable backend blocks attributed registration rather than silently discarding the code. Existing registration without a code remains available. Manual code entry/share is supported; automatic invitation-link attribution is not claimed. The existing provider PWA is retained and is not modified to expose this new native referral UI.

## Authentication prerequisite and branch reconciliation

The current deployed source has a legacy email-only Google login and a development-login route. Cash-convertible rewards must not be activated with these authentication bypasses available. This candidate retires /auth/gmail with410 before database access and makes /dev/login opt-in only for development/test; production/staging/unknown environments remain blocked. The current mobile and provider PWA source do not use those endpoints. Native Google sign-in continues through signed Privy verification.

The DevLoginAllowed/config/controller guard overlaps unmerged PR68. If this candidate is merged, reconcile/close PR68 as superseded rather than merging duplicate changes. PR66 and PR67 remain independent and unmerged. No Redis/proxy/firewall/secret settings are changed. Very old clients calling the retired endpoint require an update; do not restore insecure login as rollback.

## Compare-price correction

The catalogue formerly returned a compare price only for flash sales. List/detail/recommendation/flash queries now preserve a valid provider compare price with the same delivery fee as the displayed price, and only for a matching currency/base-price offer. Independently priced destination offers do not inherit an unrelated comparison price. Mobile displays the red crossed-out price below the current price, uses smaller adaptive text, and does not apply an inactive raw flash-sale price over the authoritative API price.

## Validation

Local compilation/security guards are separate from real PostgreSQL evidence. Isolated CI applies all migrations to an empty PostgreSQL16 database and tests5/500 totals, all four plans, concurrent payments, no repeat on renewal/upgrade, verification ordering, free/unpaid/underpaid/wrong-currency cases, rollback/self/invalid attribution, new versus existing Google identities, and public catalogue price/delivery consistency. No test targets production. Record final CI results in root HANDOVER.md.

## Release and rollback

1. Obtain approval for the reviewed API/worker/replica release and migration102. Keep automatic rollouts gated. Preserve a successful recoverable backup and the current images.
2. Apply migration102 once using the existing controlled migration mechanism; retain database advisory-lock/migration records. API/worker compatibility and replica alignment must be checked together.
3. Deploy only the approved source, retaining existing environment and secret settings. Verify readiness, legacy-route guards, authenticated referral code/stats and a designated new-account journey. Do not send real payments merely as a health check.
4. Only then produce the next normal EAS APK with the reviewed mobile commit. Do not retire the PWA.
5. To pause a faulty reward rollout, an explicitly approved operator can run UPDATE referral_program_settings SET enabled=false WHERE id=true. Validation/code issuance then fail closed and triggers stop awarding; existing balances/history remain. Registration without a code and ordinary login remain available.
6. Preserve attribution, payment records and XP ledger. Do not drop migration102/tables or delete credits as rollback. Keep authentication guards. Re-enabling after a pause requires a bounded, reviewed reconciliation of eligible pending referrals; do not run an unbounded payout/backfill job against production.

No production deployment, migration, account creation, payment, reward payout or EAS build is authorized by merely preparing this candidate.
