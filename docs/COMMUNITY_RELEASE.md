# Historical username friends, updates, promotions and Town Hall rollout

**Superseded for launch:** the user removed Friends, personal Statuses and connection
promotions. See [the current launch release](LAUNCH_SIMPLIFICATION.md) for active scope,
migration 085, the provider mobile workspace and deployment checks. The account/contact
discovery and advertising flow below is historical, not a current launch instruction.

Implementation: 14 October 2026. The previous contact-only marketplace release
remains the production baseline until this release is migrated and deployed.

## Completed implementation stages

1. Shared inbox and personal identity: unique usernames, exact opt-in discovery,
   invitations, accepted friend requests, private DMs, images, read markers,
   blocking/unblocking and reports. Existing product/service threads use their
   original business-team permissions in the same inbox. Provider staff do not
   inherit their owner's personal DMs. Usernames do not replace authentication.
2. Phone/contact discovery is explicitly deferred. There is no contacts permission,
   SMS account, OTP charge or address-book upload in this release. Users connect by
   username or invitations; both parties must accept the friendship.
3. Updates and promotions: text/photos, 24-hour expiry, friends/selected-friends
   audiences and owner deletion. Paid-provider promotions are labelled, require
   approved identity and a confirmed active paid plan, and use accepted friends.
   All users along a promotion path must separately opt in. Private statuses do
   not become public advertisements.
4. Connection reach: a worker computes paths of at most three edges, twenty
   neighbours per node and 200 cached paths, with a two-second SQL budget. Redis
   caches candidates for 15 minutes. The feed revalidates all paths, paid status,
   consent and blocks in one bounded SQL query before returning public content.
   Country/city and optional category restrictions apply to the public listing. Feed deliveries are
   capped at three per provider per viewer per UTC day and three promotions per
   response. This is a delivery cap, not click billing or a promise of impressions.
5. Town Hall: public request/offer previews, buyer-approved entry, ten concurrent
   provider seats, current verified paid eligibility, owner removal/closure and
   private DM handoff. Pending applicants do not occupy seats. Expiry immediately
   stops offers/messages; renewal restores eligibility, but removed providers
   still need fresh approval. Closed requests are hidden publicly; buyers retain
   chat and offer records. Private DMs remain independent. One applicant user
   represents each provider organisation; other staff do not silently inherit a
   Town Hall seat. Providers may join multiple independent rooms.
   Structured offers also create retry-safe room messages; choosing an offer
   records its selected summary once in the private DM. Foreground notifications
   refresh open chats immediately, with polling retained as a fallback. Social
   notification previews use generic text, keeping bargaining bodies inside the
   authenticated chat.

## Database and rollout

- Apply migrations 080–083 before the new API/worker runs. They add social profiles,
  friendships, blocks, shared rooms/messages, Town Hall, reports, statuses,
  promotion jobs and PWA push destinations; existing business history is retained.
- In Coolify, save RUN_MIGRATIONS=true on the API before deploying this release;
  worker RUN_MIGRATIONS stays false. Alternatively run the new image's
  /app/across-migrate during a controlled deployment before new consumers start.
- Deploy API, then worker from this release. Check /api/v1/ready on both. Worker
  readiness now includes social_promotions in addition to the existing loops.
- Deploy admin and provider applications separately. Provider Dockerfile includes
  the new community assets. PWA shell v6 waits for Refresh app, preserving existing
  drafts rather than activating an update while the person is typing.
- Build the mobile release from the reviewed mobile branch after backend readiness.
  The user is running EAS builds themselves; this session must not submit another
  cloud build. No new native contacts/SMS dependency is required.
- Existing Redis and private S3 image configuration are reused. No new paid service
  or provider product checkout is introduced; Flutterwave remains subscriptions.

## Validation and remaining release checks

### Town Hall lobby and support follow-up (14 October)

Migration084 adds a message_only flag and permits absent quantity/target price.
New clients create requests with one message (3-2000 characters); the server
derives a short title and stores no guessed quantities or prices. Existing
structured requests and offer/approval/seat/privacy rules remain intact. Apply084
before deploying the new portal/mobile request forms. Free-form offer amounts are
displayed without invented per-unit totals.

Provider home now has a draggable Town Hall entry point with bounded activity
refresh, hidden while typing. The lobby uses responsive request cards, buyer
names, offer counts and occupancy. A failed inbox ends its loading state; Hall
loading does not depend on profile/inbox loading. Support list and selected-thread
refreshes run concurrently, keep private cached history and avoid repeated read
acknowledgements for the same message. PWA shell is v7, activated by Refresh app.

Reported production community503 remains under diagnosis: supplied log sample
has no /api/v1/chat requests. Backend now logs database error codes/table/column/
constraint names without message contents or SQL parameters. Request matching
logs before claiming the root failure fixed. Paginated/indexed reads and bounded
caches control request size; capacity still needs representative load measurement.
API readiness also checks community_schema on its serving database, including the
message-only column and promotion jobs. Database ping alone must not declare
community ready when schema is absent or a migration URL targets another database.

Local PostgreSQL integration tests use isolated schemas on loopback only. Checks
cover identity privacy, acceptance, outsiders, idempotent sends/statuses, read
cutoffs, blocks, selected audiences, removed-friend privacy, expired subscriptions,
Town Hall previews/admission/removal/closure/handoff and concurrent seat limits.
Promotion tests exercise actual three-edge graph generation and stale-cache
revalidation after opt-out, blocking, friendship removal and subscription expiry.

Mobile TypeScript and existing chat/notification regressions pass. Isolated browser
checks cover 320/390/768/1440 layouts, dark cards, status drafts, retry references,
account reset and a reduced-height composer. Existing PWA, unread, photo viewer,
support, theme and page refresh regressions remain passing.

Before launch: verify CI, migration/deploy logs, API/worker readiness, a real mobile
and installed-PWA cross-account flow, and notification navigation in foreground and
background. Android/iOS device behaviour and high-load capacity are not certified
by JavaScript export or these functional tests. Keep production backups and restore
checks in place; this release does not claim unlimited database capacity.

Status cleanup/long-term retention, automatic moderation and provider staff
delegation beyond the applicant seat are follow-up policies. Expired/deleted updates
are excluded from feeds immediately, while their database records and private
objects are retained until an explicit retention policy is approved.
