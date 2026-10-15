# Town Hall redesign and unanswered-request expiry — 15 October 2026

## Confirmed policy

The user selected **five minutes with no provider engagement**, not five minutes since
the last interaction. A provider request to join, admission, offer or bargaining message
counts as engagement. Views and buyer messages do not count. Once any provider engagement
exists, the request stays open until its owner closes it under the existing rules. An
expired request disappears publicly; its buyer retains a read-only record and can manually
repost the message as a new request. Reposting does not reopen the old room or copy its
members, offers or private messages. There is no automatic repost loop.

Paid verified providers still require buyer approval; ten active provider places maximum.
Free verified providers can browse but cannot apply. A paid plan expiring still blocks
provider messages/offers immediately. Existing rules are not weakened by this redesign.

## Presentation and feed

Mobile and provider PWA use a compact mint/green hero, original civic-hall/conversation
vector mark, circular buyer avatars/initials, timestamps, request cards, seat counts and
offer cards with backend-authoritative provider badges. Original message entry stays one
field. Budget/location chips are only shown for actual structured values in older requests;
no amounts/routes are invented from free text. Detail shows the full original request once.

The original vector geometry is shared by mobile TownHallIcon.tsx and portal hall-icon.js.
No external image/model service is used. Prior Account-only Settings gear and removed
Track tab remain unchanged; the supplied mockup does not override those decisions.

Foreground feed refresh is bounded to the newest 25 requests every 30 seconds, conditional
on ETag, and coalesced. Background apps/tabs, request editors and open bargaining rooms do
not continuously poll the public feed. Requests insert at the top with a brief 220ms
arrival animation when already at the top. While reading further down, a Show latest
prompt queues the next head rather than reordering the visible cards. It preserves the
scroll anchor and draft. Reduced-motion settings disable the arrival animation. There is
no forced scrolling, endlessly moving carousel or continuous animation across 100 cards.
Pagination remains keyset based; mobile virtualizes feed rows. Seen-ID memory is bounded.

## Backend and migration

Migration **086_town_hall_idle_expiry.sql** adds a fixed engagement deadline, first-provider
engagement timestamp and expiry timestamp. It backfills evidence from existing applications,
provider participants, offers and messages so previously engaged requests are preserved.
It never deletes requests, conversations or messages.

Application/admission triggers and the expiry worker serialize on the same room row.
Late applications are rejected in both API and database. Expiry batches recheck that no
engagement was recorded, use SKIP LOCKED, and close at most 250 requests per worker cycle
(every 30 seconds, 10-second timeout). Multiple workers can safely share the job. The new
worker health loop is town_hall_expiry. Public list/activity/detail and write checks apply
the deadline immediately even before physical closure by the worker. Only the buyer can
read the expired public request record; existing buyer conversation history stays readable.
Actual UI removal may wait for the next foreground refresh.

API readiness checks the new database function. Apply migration086 on API before starting
the new worker or deploying the updated clients. Existing API RUN_MIGRATIONS=true setting
was previously confirmed by the user; worker RUN_MIGRATIONS remains false. No new secrets
or database service are required. If those settings have since changed, restore them before
the deployment. Do not run a database restore or delete old request data.

Deploy API first, verify migration/readiness, then worker (town_hall_expiry:true), admin
and standalone provider. Provider shell becomes 20261015-v12; installed users select
Refresh app. User runs their new EAS build from mobile main; Codex submits no EAS build.

## Validation and limits

Tests cover unanswered expiry before sweeper, read-only buyer history, private closed
records, manual repost with a new ID, paid engagement exemption, late-entry database/API
rejection, concurrent application/worker locking, worker health and original seat/plan
rules. Browser tests cover queued arrivals/scroll anchor, reduced motion, edit preservation,
reposting, actor cleanup and 320/390/768/1440 layout. Native feed tests cover polling
coalescing, editor pause, explicit reveal and teardown. Record final CI/export publication
in root HANDOVER.md. Physical phone acceptance and high-concurrency production-sized load
tests remain separate; no capacity guarantee for thousands of simultaneous users is made.
