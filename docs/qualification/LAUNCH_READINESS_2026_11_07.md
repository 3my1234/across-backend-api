# Launch execution checkpoint, 7 November 2026

The 500-session campaign is closed at the owner's direction. No additional VPS
setup/load, production deployment/migration/restart, DNS/S3 change, paid service,
PR merge or store publication is authorized by this checkpoint.

## Why100 passed and20 failed

The100-session pass42359825324 was disposable CI:180seconds, p95 81.712ms,
1507 writes exactly once. Latest real VPS run42379386686 used the same verified
cf992a4 executable but aborted its20-session45-second stage after about15seconds:
p95 634.874ms, catalogue2053.566ms, services1944.965ms. These are different
hardware/network/cache conditions and durations, not a controlled scaling curve.
The actual VPS100 stage has never passed its full qualification. Earlier VPS20
passes used earlier builds/runs. No maximum supported concurrent-user count follows.

The latest request evidence confirms backend work as well as transport cost:
gateway catalogue median backend response1023ms (9requests), maximum1584ms;
services maximum2246ms (9requests). SQL catalogue3calls mean558.23ms; service5calls
mean131.16ms. Pools accumulated only151ms additional wait across both replicas
between sampled endpoints; four database samples showed no lock waiters. These
coarse observations do not establish CPU/IO/cold-cache causation or exclude brief
contention. No new SQL rewrite or pool increase is justified by these values alone.

The worker completed43jobs/259notification rows, zero pending/failed/missing,
max lag205.914ms.42ACKed writes persisted once;43offered keys were stored once,
with one lacking ACK at abort. Normal completion/reconciliation did NOT pass.
In-flight cancellation is consistent with this inventory but not proven to be the
cause. Keep idempotency and the unconfirmed classification. Cleanup audit recorded
zero owned resources/keys, stopped units, production containers healthy and API200
at that observation time; this is not an ongoing health guarantee.

## Release source reconciliation

- Backend baselinecf992a4 contains PR85 commit8b7ebe7. It is not an unmerged
  optimization missing from this candidate despite PR85 still being open.
  Current main30b0b68 is its ancestor. No ordinary dirty checkout was reset.
- Mobile baselineb54e675 includes mainee7546c and stable message IDs/Retry-After.
- PWA/admin latest main068ff62 was merged into the separate candidate, preserving newer billing and tier work; original tmp files
  remain untouched. New work is on named launch-readiness branches in all3repos.
- Production image/config/schema versions are not inferred from local HEAD or CI.
  They require the existing owner-approved deployment inventory/signoff process.

## Implemented in this checkpoint

1. Additive migration111 changes NEW video allowances: Gold only,1/listing,
   5/provider,30seconds,50MiB. Database triggers serialize provider-wide quota
   accounting across products/services and retain existing videos on ordinary edits
   and paid downgrades. Policy is configurable, including future Diamond access.
2. Pending-byte reservations, rolling daily authorizations, pending-count/storage
   caps; signed exact upload length; expiring verification lease; atomic asset and
   ticket completion; idempotent completion replay. No network call holds the
   provider-row transaction. New mobile/PWA clients send actual file_size.
3. New registered video URLs redirect to short-lived S3 GETs rather than proxying
   video bytes through API/default image CDN. Existing published URLs are retained.
   Proposed lifecycle JSON is limited to a NEW temporary-video namespace; not applied.
4. PWA preserves over-limit existing videos when no new file is selected. Mobile
   respects current policy/pause and no longer falsely labels every video60seconds.
5. Production example now uses PostgreSQL verify-full and Redis TLS, explicit
   trusted-ingress configuration, separate workers/migrations and per-process budgets.
   No production environment variable was changed.
6. Offline budgets cover replicas/distinct read pools/admin reserve and100/1000/10000
   providers with varied usage/cache assumptions. Examples are UNVERIFIED inputs,
   not current AWS prices or account bills. Video direct-S3 egress is never reduced
   by the image cache-hit rate. No claim of profit or thousands of concurrent users.

## Finite remaining launch work and verification

| Area | Current evidence / affected files | Remaining gate and verification | Safe offline? |
|---|---|---|---|
| Core auth, chat, Town Hall | Existing bounded admission, auth, locks, retries; controllers/services/routes | Exact candidate PostgreSQL/race regressions; device same-account/retry acceptance | Tests yes; production signoff separate |
| Catalogue/services | PR85 and prior search/cache changes already included; retained VPS SQL/gateway logs | Environment-dependent slowdown remains unexplained; do not infer capacity or raise pool limits | Retained-data analysis yes |
| Pool budget | internal/db/db.go; deploy/launch/connection-budget.example.json | Actual deployed processes/read pools/max_connections/admin reserve; example is not installed config | Calculator yes; inventory read-only |
| Polling/realtime | Conditional history, foreground/no-overlap polling, durable DB messages | Keep polling. No measured case for a launch WebSocket/Redis-GEO rewrite.100screens at5sec offer20reads/sec; actual mixed replay used15sec, not5sec | Client/regression checks yes |
| Media allowances/costs | migration111, provider_video_reservation.go, launch_budget.py | Owner selected stricter NEW uploads with old media retained. Actual bucket region/rates/FX/Cloudflare contract and S3 exact-length PUT/device playback acceptance remain | Policy/model tests yes |
| Provider image pipeline | Migration113, provider_image_processing.go; Linux validation42399983916 PASS | Opt-in new-upload pipeline verified for signature/pixel bounds, EXIF orientation, no upscaling, transparency, three variants, ownership and durable publication. Keep flags off until client rollout, lifecycle and worker resource review. Recent-image recovery UI and thumbnail adoption across feed surfaces remain outstanding | Yes |
| Video pipeline | Opt-in durable720p/30FPS H264/AAC processing, thumbnail, fenced claims, bounded dedicated worker and recent-job recovery implemented | Exact-source Linux FFmpeg/race validation and real S3/device/resource acceptance remain distinct; feature stays OFF until owner-approved rollout | Nonproduction tests yes |
| Media cleanup | Only new pending lifecycle proposal prepared | Reference-aware orphan/superseded-object/version-retention review; no broad deletion or lifecycle replacement | Dry-run code/tests yes; actual rules approval |
| Payments/subscriptions | Existing gateway authenticity, checkout expected amount, receipt/idempotency tests | Exact candidate regressions plus real payment-provider catalog/webhook/reconciliation acceptance; no payment succeeded merely because UI said so | Mock/local tests yes; real provider signoff separate |
| Store billing | Existing Play-native flow and iOS purchase guard in mobile candidate | Actual Play product mapping/test purchase/account credentials; Apple review of digital provider benefits vs physical goods. Physical checkout exemption does not automatically exempt digital listing upgrades | Source tests yes; account/device acceptance separate |
| SES/push | Durable queue rows tested, historical receipts/worker coverage | Actual SES sending status, complaint/bounce processing/alert receipt and Android/iOS delivered notifications | Offline mocks yes; real delivery not proven |
| Backups/recovery | Historical real archive restore passed22Oct:16.12seconds,101migrations | Current production backup freshness/retention/off-server access and migration111-compatible restore/rollback; old drill is not proof of current recovery | Isolated restore yes if archive available |
| Security/operations | Existing validation, safe retry/admission, separated roles, monitoring routes | Current exposed ports/TLS/CA/secret configuration, external alerts/incident owner, dependency scan and no sensitive logs | Source/config checks yes; host/account signoff separate |
| Android/iOS artifacts | Mobile source typechecks; Play AAB profile/source guards | Signed AAB, physical device acceptance, Apple enrollment/signing/macOS build, privacy/data-safety/deletion/screenshots/review credentials. No signed store artifact was created by earlier source-export checks | Source/export yes; signing/account work separate |
| Controlled launch | No numerical safe capacity established | Owner-approved onboarding waves with active alert owner, stop expansion on sustained latency/errors/backlog or correctness failure, pause new activity and roll back compatible image if needed | Runbook yes; deployment approval separate |

## Controlled launch decision

**Not yet approved for public production rollout.** This checkpoint supplies concrete
source corrections; it does not complete the attached21-part launch assignment.
Finish the remaining image-processing deliverables and operational/store acceptance
above, or obtain an explicit product decision to defer those features while retaining
existing paid media. Do not declare video processing complete because verification
passes. A controlled launch cannot be justified solely by disposable100-session results.
500/1000/10000 concurrent-session capacity remains unverified.

The owner need not repeat setup, upload a qualification package or run another
capacity command. Production and store actions remain the final approval step after
these specific artifacts and operational acceptance are reviewable.

Official sources checked7Nov2026:
https://aws.amazon.com/s3/pricing/ ;
https://www.cloudflare.com/service-specific-terms-application-services/ ;
https://support.google.com/googleplay/android-developer/answer/9858738 ;
https://developer.apple.com/app-store/review/guidelines/ .
Store policy application is an engineering assessment; actual account/market exceptions
and review approval are not assumed.


## Validation checkpoint

5abd51c passed exact-source PostgreSQL16, actual FFmpeg verification and race checks
in42385450546. New durable processing at7be4c5f passed its actual1080p-to720p,
thumbnail and once-only publication test in42397214229, but the whole run failed a
worker monitor inventory assertion. The monitor has been corrected to distinguish
explicit-only media from the11default consumers; a fresh exact-source check is required.
No failed run is relabelled as a successful release or capacity qualification.


## Consolidated review checkpoint

Backend application source4dde05a passed complete Linux/PostgreSQL16/FFmpeg,
correctness and race validation in run42399983916. Mobile c11f1ed passed both
PR checks (42400105824,42400105885), including TypeScript and50 provider suites.
Portal17bd9b8 passed all three checks (42400125307,42400125344,42400125384).
Draft PRs: backend87, mobile62, portal57. None is merged or deployed.

Linux amd64 API, worker and migrator review binaries were built from4dde05a with
CGO disabled, trimmed paths and embedded source revision. Files and SHA256 manifest
are retained outside Git at qualification/launch-release/. No VPS package was
uploaded or live operator started. Subsequent documentation/budget-only changes
are separately verified with the five budget tests; they do not change those binaries.
The media budget now counts three stored image variants/PUTs and uses16MiB as a
conservative combined-image bound. This is illustrative, not a measured bill.

Decision: reviewable release candidate; production launch approval remains blocked
on actual release inventory and backup/rollback verification, payment-provider and
subscription acceptance, real SES/push delivery, signed store binaries and device
acceptance. Image processing stays disabled until its rollout prerequisites above
are satisfied. No supported concurrent-user number is asserted. Do not resume the
closed load-test campaign or silently enable processing to bypass these gates.
