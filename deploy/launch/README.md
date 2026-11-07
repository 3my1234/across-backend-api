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
3. Apply the reviewed missing schema migrations through 111 using the separate
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
registration and redirects to a one-minute signed S3 URL with `private, no-store`.
Video bytes go from S3 to the viewer, avoiding API proxy CPU and assumptions about
standard Cloudflare video CDN eligibility. S3 transfer remains chargeable. The actual
bucket/Cloudflare routing and Range playback need owner infrastructure acceptance.
Old published URLs remain unchanged. Public image CDN behavior is unchanged.

This is verification plus upload-cost protection, NOT the requested complete 720p
transcoding/thumbnail pipeline. The existing endpoint still performs bounded ffprobe
verification in the API, does not downscale 4K, and does not compress all uploaded
images. A durable, separately CPU-limited FFmpeg worker with verified output and safe
cleanup is a remaining media deliverable; do not advertise it as complete or enable
large-scale paid-video onboarding based on this patch. Do not improvise another API
thread running unrestricted transcoding. Existing click-to-play behavior is retained.

## Connection budget

Run `python scripts/launch_budget.py connections deploy/launch/connection-budget.example.json`.
The example totals 24 application connections + 6 other clients + 10 emergency/admin
connections = 40. It assumes two API replicas at8, general worker4, fanout worker2 and
migration2, no distinct read pool. It MUST be replaced by actual process inventory and
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
