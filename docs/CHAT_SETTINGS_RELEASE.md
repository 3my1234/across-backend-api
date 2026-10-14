# Settings and cached-first chats — 14 October 2026

## Scope and findings

The user requested a gear-icon Settings area on admin, provider and mobile, faster
chat opening, and private-chat presentation based on the supplied WhatsApp screenshots.
Town Hall keeps its distinct compact room presentation. No voice/video/call buttons
were added without implementing those features.

Inspection found memory-only caches with a five-minute lifetime, loss of history on
process restart, an extra inbox lookup before opening a known provider conversation,
mobile read acknowledgements holding its loading state, and render-blocking map assets
on every provider page. These findings do not establish PostgreSQL as the bottleneck.

## Implemented

- Gear Settings: mobile appearance, notification preferences, phone settings, profile
  editing, sign-out and saved-chat storage; provider appearance, phone notification/
  install controls, verification documents, subscription access and sign-out; admin
  appearance, administrator management and sign-out. Existing role checks and IDs/
  handlers are preserved. Operational work such as moderation/support stays in its area.
- Private-chat inboxes show circular avatars/initials, counterparty names, previews,
  last-message times and unread counts, with search over loaded recent conversations.
  Private rooms use readable green outgoing/neutral incoming bubbles, compact times
  and a fixed composer. Town Hall does not acquire personal-status/friend controls.
- Authenticated inbox responses add preview text and peer avatars using existing
  columns/indexed message lookups. The outsider test verifies previews stay private.
  Provider-side rows identify the buyer, rather than showing the provider's own badge.
- Recent inbox/thread snapshots survive restart: AES-256-GCM with independent random
  nonces, a device-only SecureStore key and encrypted AsyncStorage on mobile; WebCrypto
  AES-GCM and nonextractable IndexedDB keys on provider PWA. Native embedded provider
  snapshots use the nonce/origin-guarded bridge and native encrypted store, so incognito
  WebView isolation can remain enabled. Session tokens are not persisted as cache IDs.
- Snapshots are session/account scoped, bounded to 12 entries and 256,000 serialized
  characters, with a seven-day read lifetime. Persist only the latest fetched 40-message
  page and its matching older-page cursor; Town Hall room histories are excluded.
  Logout removes the saved data/key and blocks late writes. Storage failure/corruption
  falls back to live reads. Encryption at rest is not end-to-end message encryption.
- Cached UI appears while server requests run; a known inbox chat avoids another lookup.
  Refreshes/read acknowledgements do not lock sending. Native history reads coalesce;
  loaded earlier-history cursors survive a latest-page refresh. Server authorization
  remains authoritative. There is no offline send queue or whole-history offline backup.
- Provider account/profile requests overlap; map code loads only when editing a listing,
  so a slow external map CDN no longer blocks Chats. Admin startup calls the existing
  configureRoleUi function in place of an undefined setNavVisibility call.

## Deployment and validation

No new schema migration or MongoDB service is required. The previous migration 085
requirement still applies if that release has not yet been deployed. Deploy the updated
API, admin and standalone provider resources. This release does not change the worker.
PWA shell is 20261015-v10; installed users choose Refresh app. Then the user runs their
new EAS build from mobile main. No cloud build is submitted by Codex.

Checks include isolated PostgreSQL inbox privacy; mobile encryption/tamper detection,
restart/account isolation/logout/late writes; trusted-origin native cache replies; PWA
IndexedDB encryption and reload; actual cached inbox/thread rendering while simulated
network requests never return; no extra lookup for a known chat; Settings node placement,
role-preserving admin controls, lazy-map startup; keyboard/responsive chat/photo tests;
mobile type checking and Android/iOS JavaScript export. Record publication/CI results
in the root handover after review.

First installation, uncached/older chats and new messages still need the server. Local
cache is a speed layer, separate from PostgreSQL backups. Representative load tests and
physical Android/iOS/PWA checks remain required; no WhatsApp-scale capacity is claimed.

Reference: Meta describes WhatsApp linked devices reading their own local message
database: https://engineering.fb.com/2021/07/14/security/whatsapp-multi-device/
