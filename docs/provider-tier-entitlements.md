# Provider tier entitlements (unreleased)

| Plan | NGN/month | Shared listings | Images/listing | Concurrent requests | Promotion weight |
|---|---:|---:|---:|---:|---:|
| Bronze | 500 | 3 | 3 | 3 | 1 |
| Silver | 1,000 | 10 | 5 | 5 | 2 |
| Diamond | 2,000 | 25 | 8 | 8 | 3 |
| Gold | 5,000 | 60 | 12 | 10 | 5 |

All paid tiers retain verified-provider Town Hall search/category/location filters, messaging without new quotas and relevant recommendation eligibility. Promotion remains labelled advertising and relevance-first, deduplicated and diverse. Relative weights do not guarantee traffic or sales. Room capacity remains 10 providers, independent of these business-wide participation limits.

Free remains one combined listing, one image, no videos, no provider Town Hall participation. Provider verification remains required. Unarchived drafts reserve listing capacity, as with migration097. Existing video entitlements by price are retained: Diamond2 and Gold5 verified videos, each at most60 seconds. Bronze/Silver have no videos.

## Compatibility

Migration107 updates display names, badges and limits for the same four plan records. It does not change plan IDs, codes, prices, Flutterwave links, Play product/base-plan IDs, historical payments or periods. `provider-business` (NGN2000) now means Diamond and `provider-diamond` (NGN5000) means Gold. Play Console display names should agree when store products are eventually configured; billing stays disabled pending that separate setup. No payment initiation or client-supplied tier activates benefits.

`provider_current_benefits` resolves the existing receipt-backed current period. `/providers/me/entitlements` returns benefits, usage and remaining capacity. The existing `/post-policy` adds the same usage while preserving old fields. Public/admin/Play catalogues return actual plan benefits. Nonstandard legacy paid plans retain their configured listing allowance.

Database provider-row locks serialize mixed product/service creation and distinct active Town Hall admissions across staff. Closed, exited and deadline-expired rooms do not count, even before the cleanup worker runs. Existing room membership and customer-mode request creation remain intact; self-response stays forbidden.

Paid downgrades keep existing publications and media visible. New/unarchived listings and first publication must fit the current allowance. Over-limit existing media may be kept or reduced, but cannot be replaced or expanded beyond the allowance. Existing conversations and joined requests remain. Expiry uses the existing Free presentation rule, without deleting records. Upload preflight checks owned existing targets; old clients can still prepare uploads before creating a draft. Final media attachment always passes atomic database checks, irrespective of upload metadata; presigning does not reserve a media slot or authorize extra attachments.

## Release gate

Not merged or deployed. Production remains schema106. Separate approval is required for migration107 and release. Qualify the final exact source and fresh backup/rollback capture, pause the existing reconciliation timer, run107 with the bounded migrator and independently verify ledger/plan IDs/prices/schema. Then release compatible worker/API, reconcile both replicas using the existing reviewed controller, verify readiness and resume the timer. Release the provider/admin web changes only after API compatibility; admin auto-deployment is currently enabled, so do not merge its PR prematurely. User triggers the new mobile EAS build after release verification. Do not alter Redis, VPN, proxy or billing configuration for this feature.

An application rollback must account for the new tier labels and quota semantics; do not drop triggers or delete provider data to roll back. Retain a known compatible application image and preserve107 until an explicitly reviewed corrective migration is required.

## Validation

Isolated PostgreSQL tests cover all tier limits, receipt/period states, concurrent mixed creation and room admissions, downgrade retention, archive/reactivation, upload ownership, weighted rotation, real membership API conflicts and same-account customer requests. Historical quota fixtures continue testing their original migrations. Native tests cover server-backed benefits, older-API fallback and retained media, alongside checkout/recovery and Play transaction regressions. PWA Chrome tests exercise all plan cards, annual quotes, renewal controls and responsive themes. Live Play purchases and physical-device acceptance remain separate.
