# Launch candidate: explicit review boundaries

No file in this directory deploys, changes production, purchases infrastructure,
or starts a load test. `connection-budget.example.json` and `media-cost.example.json`
are assumptions, not installed inventory, live prices, exchange rates or bills.

## Deploy order (owner approval still required)

1. Preserve the actual deployed image/config and take a recoverable backup. Verify
   installed migration inventory before selecting the missing additive migrations.
2. Deploy compatible mobile/PWA clients with `file_size` video metadata first.
   Old clients can continue core operations but cannot request NEW video uploads
   without that metadata. Existing published video URLs remain readable.
3. Apply the reviewed missing schema migrations through 114 using the separate
   migration command; never run API replicas as automatic migrators. Review the
   new indexes' effect and migration lock budget before production approval.
4. Deploy the API/worker images built from the reviewed release SHA. The optional
   durable notification producer remains OFF by default. If enabled later,
   schema and the bounded message_fanout consumer must be ready first.
5. Verify backend identity on every replica, payment/entitlement reads, core writes,
   production email/device delivery, actual S3 permissions and signed upload lengths.
6. Roll back producers first if necessary. Keep consumers draining committed durable
   jobs. Do not drop jobs, receipts, video assets, upload reservations or the additive
   schema as part of an application rollback. Keep API/backend/client compatibility.

## Video rules and preservation

Migration111 defaults: Gold only; one new video per listing; five active attachments
across unarchived products and services; 30 seconds; 50MiB per incoming video.
Existing unchanged/removed/reordered videos survive policy and paid-plan changes.
Reactivation of an archived listing consumes the current allowance. Existing prices,
plan IDs, paid periods and all non-video tier benefits are unchanged.

`provider_video_launch_policy` holds the configurable duration, per-listing/provider
counts, optional future Diamond access, upload pause, 250MiB registered+reserved
storage allowance, ten upload authorizations per rolling day and two pending uploads
per provider. Initial numbers are conservative policy choices, not profitability proof.
Changing this row in production requires reviewed owner approval. Pause affects new
uploads/finalization/attachments; existing registered video viewing and completed
request replay stay available. The registered storage cap counts unattached assets
until a reference-aware deletion process removes them; there is no destructive cleanup
in this candidate and the cap may therefore require support intervention.

Presign reserves the bytes before returning a URL, and signs the exact Content-Length.
Completion claims a bounded lease, verifies the actual file, publishes a stable fresh
object never signed for client PUT, and atomically registers it with ticket completion.
Replaying a completed ticket returns the original URL. The 3-minute claim lease exceeds
the 75-second verification bound. Ticket expiry is 20 minutes; PUT authorization is
15 minutes. S3 is not called while a database transaction holds the provider lock.

`video-temporary-lifecycle.review.json` is a PROPOSAL only. Its one-day expiration
covers ONLY the NEW `private/video-pending/` namespace. It cannot remove verification
documents or published objects. Merge the reviewed rule into the bucket's existing
lifecycle configuration; do not replace unrelated rules. Versioned buckets require a
separate noncurrent-version cost/retention review. Older temporary namespaces and
unregistered verified objects require a reference-aware inventory; no broad prefix
cleanup is approved.

## Media delivery and outstanding processing

New stable video URLs use `/api/v1/public/videos/view/...`. The handler verifies asset
registration and redirects to a five-minute signed S3 URL with `private, no-store`.
Video bytes go from S3 to the viewer, avoiding API proxy CPU and assumptions about
standard Cloudflare video CDN eligibility. S3 transfer remains chargeable. The actual
bucket/Cloudflare routing and Range playback need owner infrastructure acceptance.
Old published URLs remain unchanged. Public image CDN behavior is unchanged.

The opt-in `ASYNC_VIDEO_PROCESSING=true` path now queues durable video jobs.
Migration112 adds fenced claims, a global one-encoder lease, at most10queued/inflight
jobs, three bounded attempts and status recovery. `WORKER_ROLES=media` is explicit-only;
legacy `all` workers do not start an encoder. The media worker must be separate, at
most2DBconnections, with the reviewed0.5CPU/512MiB/pids64/tmpfs192MiB constraints.
These are proposed limits, not an approved production resource allocation.

The worker does not hold a database transaction while doing network/FFmpeg work.
A180-second job deadline is shorter than its240-second claim lease. Failed phases are
recorded without signed URLs or message content. Output H264/AAC MP4 is bounded to720p,
30FPS,16MiB with fast-start metadata, and decoded before publication. Portrait/landscape
aspect ratio is preserved. Compliant inputs are remuxed rather than re-encoded. AJPEG
thumbnail is generated at400px maximum. Both objects must upload before the atomic
asset/ticket completion. Claim tokens fence stale publishers. Source deletion is only
after commit; old/public objects are not destructively cleaned up.

Mobile/PWA support202status responses, bounded GET-only polling and a recent-upload
list to recover after closing the screen. The synchronous legacy path remains when
the feature is OFF. Existing completed tickets can be replayed after a plan changes.
Do not roll back to a binary predating background-ticket awareness while jobs remain;
turn off NEW production first, keep a compatible API/status route and let consumers
finish committed jobs. Never roll back by dropping queues/schema.

`/api/v1/admin/ops/video-queue-health` uses the existing catalog-admin authorization;
it reports aggregate counts, lease state, failure/age attention flags, not asset keys.
Wire external alert receipt before enabling in production. This source work does not
prove real S3/Cloudflare/account permissions, real-device playback, long-run encoder
headroom or a complete image-processing pipeline. Public-provider image compression,
orientation, thumbnail generation and EXIF stripping remain separate work; do not
claim those are implemented because the video test succeeds.

## Connection budget

Run `python scripts/launch_budget.py connections deploy/launch/connection-budget.example.json`.
The example totals 26 application connections + 6 other clients + 10 emergency/admin
connections = 42. It assumes two API replicas at8, general worker4, fanout worker2, optional dedicated media
worker2 and migration2, no distinct read pool. It MUST be replaced by actual process inventory and
PostgreSQL max_connections before production signoff. Other applications sharing the
same PostgreSQL cluster count; Coolify's separate PostgreSQL cluster does not.
A distinct read pool counts in addition to the primary pool. Do not add PgBouncer or
raise every pool to mask slow queries. Existing lifetime45min, idle5min and room
admission bounds remain.

## Cost model

Run `python scripts/launch_budget.py media deploy/launch/media-cost.example.json`.
Rates and NGN/USD are deliberately marked UNVERIFIED illustrative inputs. The model
covers100/1000/10000 providers, light/moderate/heavy viewing and0/85/95% cache hits.
It charges CDN bytes for all viewer deliveries and origin bytes for misses, includes
request/storage/encoding assumptions, and keeps gross subscription revenue separate
from profit. The example keeps direct-S3 video cache hit rate0 and video CDN cost0,
independent of image cache-hit rates. Video size is modeled at the existing50MiB
maximum, not hypothetical compressed output. Actual AWS region/account plan/prices/taxes/credits and CDN contract
must replace examples. Default code region eu-north-1 is not proof of the bucket region.

Sources checked7Nov2026: https://aws.amazon.com/s3/pricing/ ;
https://www.cloudflare.com/service-specific-terms-application-services/ ;
https://developers.cloudflare.com/fundamentals/reference/policies-compliances/delivering-videos-with-cloudflare/ .
Cloudflare Free/Pro/Business CDN terms do not grant unrestricted external video delivery.


Provider images (review candidate, not deployed)

Migration113 and ASYNC_IMAGE_PROCESSING add owner-scoped durable processing of new
listing, logo and banner uploads. Existing published images remain unchanged.
The dedicated media worker validates signatures and decoded dimensions, corrects
EXIF orientation, strips metadata, preserves aspect ratio without upscaling and
produces a WebP display image,400px WebP thumbnail and JPEG/PNG fallback. All
three outputs are verified before atomic registration. Display images are bounded
to1600px (logos512px); inputs10MiB and40million pixels, outputs16MiB total.
The same global encoder lease keeps video and image conversion sequential.
The database image_processing_required flag must stay false until all upload
clients are released and worker/storage/lifecycle settings have been reviewed.
Private image-pending lifecycle rules are a proposal, not an applied bucket rule.
Actual storage estimates must count all three variants; processing does not prove
an AWS bill or a production storage ceiling. Image recovery after a polling timeout
and thumbnail selection across all browsing surfaces remain release-review items.


## Renewal crash recovery and migration114

Provider renewal recovery is an explicit owner action, not a background cancellation
replay. GET /providers/me/subscription-renewal reports uncertain until reconciliation.
Use Retry cancellation (POST the same endpoint with an empty next_plan_id) after the
previous operation's two-minute DB lease expires. The API reads the active gateway
subscriptions scoped to the original verified transaction/email. If cancellation
already succeeded, it sends no further cancellation PUT and confirms the local audit
and preference atomically. Unknown/error responses remain uncertain and preserve
paid access through its legitimate expiry; a replacement checkout stays blocked.
There is no claim of exactly-once external network delivery under ambiguous gateway
responses. The UI's Retry cancellation remains available after paid access expires.

Migration114 adds nullable lease fields, a pair constraint and an update/delete
fence on provider_renewal_preferences. Existing rows retain their status/entitlements.
The separate migrator uses bounded lock/statement timeouts; a busy DDL failure must
stop the rollout rather than force a restart. Keep old healthy replicas serving
until the exact-source migrator has succeeded and inventory confirms114. New
production/staging APIs now fail startup if lease columns/constraint/enabled fence
are absent. This is a read-only startup gate, never an automatic migration.

During a rolling release, old renewals already holding the advisory lock exclude
new reservations. New active leases reject legacy preference updates before the
old API can make a gateway cancellation. Such legacy requests can receive an error
and must be retried after the operation; do not promise error-free mixed-version
renewal writes. Do not drop the added schema during rollback. Drain/expire operations
and reconcile uncertainty; keep the additive fields/fence for an older API rollback.
Actual production migration inventory, backup and DDL lock duration are not verified
by a disposable test. No deployment is authorized by this document.
