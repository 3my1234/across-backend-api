# Chats, Town Hall and provider workspace — 14 October 2026

This release supersedes the Friends/Statuses/promotion rollout in COMMUNITY_RELEASE.md.
The user explicitly removed those launch features. Historical data remains intact.

## Implemented

- Mobile and provider portal expose Chats and Town Hall. Friends, username discovery,
  invitations, personal statuses and connection advertising have no public launch
  routes/UI. The promotion worker no longer runs. Existing private conversations,
  blocks, reports, business-team permissions and paid-first catalogue discovery remain.
- Service enquiries use Message provider, without a separate booking form or new
  availability selection. Provider navigation calls this Chats too. Historical booking
  and paid-order records are retained; this is not a financial-history deletion.
- Provider onboarding/new listing forms fill empty contact fields from the saved
  account/provider profile and preserve edited values. The supplied Word document
  provides 37 groups/820 services, with three hotel/party event aliases added (823).
  The searchable picker renders at most 50 suggestions and does not query per keypress.
  Existing listing types stay compatible; category text participates in service search.
- The actual Town Hall room uses compact coloured name labels and inline messages,
  a topic heading and a pinned composer, based on the supplied 2go reference. Requests
  use one free-text field. The floating Hall button uses the same Ionicons people
  artwork as mobile. Existing admission, ten-seat limit, paid-provider eligibility,
  expiry restrictions, removal, private handoff and closed-room privacy remain.
- Provider chat retains the v8 full-visible-viewport correction, coalesced history/read
  requests, unchanged-DOM refreshes, pagination and draft preservation. Support now
  shares overlapping history requests and releases sending independently of refresh/read
  latency. Unrelated provider datasets no longer load on every initial page.
- The main mobile Account page opens the existing provider portal in an isolated
  first-party WebView with the same session. Session injection is origin-guarded and
  never placed in URLs; theme, signout and native notification navigation are bridged.
  This reuses the portal interface, not a native rewrite or an offline message store.
- Migration 085 sends provider marketplace events through existing user notifications
  and Expo delivery queues to active provider members, without repeating VAPID delivery.
  Notification bodies stay generic. No Firebase file or EAS credential changes needed.

## Billing boundary

Flutterwave subscriptions continue in the standalone browser/PWA. The embedded mobile
workspace displays existing access but does not expose subscription purchasing or a
link directing users to web checkout. Digital listing/visibility subscriptions require
a separate store-billing assessment/implementation before enabling native purchases.
This release does not certify store acceptance. Official references:
https://developer.apple.com/app-store/review/guidelines/#in-app-purchase
https://support.google.com/googleplay/android-developer/answer/9858738

## Deploy in order

1. Backend API: RUN_MIGRATIONS=true, as already authorized by the user; deploy this
   release and verify migration 085_provider_native_notifications.sql is recorded in
   schema_migrations on the serving database (10.77.0.2). `/api/v1/ready` must report
   `ok:true`, `community_schema:true` and `provider_notifications:true`.
2. Worker: deploy the same backend revision with RUN_MIGRATIONS=false. Its readiness
   should show seven healthy loops. The absent social_promotions loop is intentional.
3. Deploy both admin and **standalone provider** resources from the new portal revision.
   Shared GitHub repository does not make the two Coolify deploys automatic together.
   Verify public provider-native.js/service-categories-data.js/hall-icon.js and shell v9.
   Installed PWA users choose Refresh app; no reinstall is expected.
4. Sync mobile main and let the user run their EAS build. No cloud build was submitted
   by Codex. Existing APKs do not gain these UI changes merely by backend deployment.

During investigation the live provider shell was still v7 and lacked the merged v8
chat correction. A merge/local test alone must not be reported as a production fix.

## Validation and remaining acceptance

Automated checks cover isolated PostgreSQL provider notification recipients/outbox,
no duplicate web push, retired/retained route registration, Town Hall authorization,
seat/admission/expiry rules, mobile type checking, chat/notification/history regressions,
provider form defaults, subscription browser/native separation, native session origin
and bridge policy, and responsive browser chat at 320/390/768/1440 widths with a 450px
keyboard viewport, long history, delayed receipts/history, retries and account isolation.

Device checks still required on the new build/installed PWA: registration and saved
contact fields; create and approve an event listing; two-way text/photos; keyboard,
scroll and resume; unread-to-read and exact notification destinations; native provider
onboarding/uploads/location access/theme/signout; foreground/background push; Town Hall
admission, full/removed/expired provider cases and private handoff. Test on Android and iOS.
Functional tests do not establish a high-traffic capacity limit; run representative
concurrent chat load measurements before promising capacity.

Replacement paid-provider adverts remain a future decision. New service-review
eligibility/completion after removing bookings also needs a policy: existing verified
historical reviews remain; no unverified completion/reward mechanism was invented.
