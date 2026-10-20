# Provider chat, notification identity and email branding — 21 October 2026

## Behavior and data preservation

Migration 099 adds a shared thread identity for each customer/provider pair.
Existing per-product conversation rows and every message id remain intact. The
oldest conversation is the stable inbox identity; aliases still authorize and
load the complete shared history. No messages are moved or deleted. New enquiries
for another product/service reuse that identity, with serialized first-contact
creation. Town Hall rooms remain separate.

Per-message product/service titles and ids are snapshotted as context. The PWA
and next mobile source build display an About label. Replying in the shared
thread follows its latest physical product conversation; unread state and receipt
lookups cover all member conversations. Existing blocked conversations cannot be
bypassed by contacting another product of the same provider. Existing APKs can
use grouped history; the new context labels require an updated app build.

Migration 100 snapshots public sender identity into native/browser notification
jobs while preserving existing destinations and private message-body rules.
Android's existing Expo notification builder supports a remote large icon; the
sender's public image is supplied through richContent.image. The PWA worker uses
the same image as its notification icon and retains the branded badge. The API
produces a 128-pixel circular PNG with transparent corners; fetch and decode
concurrency are bounded to two each, dimensions to 16 megapixels, and the cache
to 128 entries. Invalid/private/signed or untrusted source URLs are excluded.

These changes do not add iOS Communication Notifications/Siri Intents. Native iOS
communication-avatar presentation requires a notification service extension and
an iOS build. Phone/browser layouts and image availability control final display;
no physical-device appearance has been certified by source/unit tests.

## Email changes and deliverability evidence

Transactional emails attach an optimized 380-pixel copy of the bundled PNG using a CID multipart/related MIME
part, with plain text and HTML alternatives and a visible text brand fallback.
The public logo endpoint serves the same embedded asset. Remote image loading
is no longer required to display the email logo in clients supporting inline
images. HTML/plain text use quoted-printable encoding so long template lines do
not violate SMTP line limits. SMTP recipients and verification-token handling
are unchanged; no real email was sent by this work.

Public DNS read during investigation:

- `_dmarc.atlxpres.com`: `v=DMARC1; p=none; adkim=r; aspf=r; pct=100`.
- `atlxpres.com` SPF: `v=spf1 include:spf.efwd.registrar-servers.com ~all`.

This is not proof of failed SES authentication. SES may use its default envelope
sender or a separate MAIL FROM subdomain; aligned domain DKIM can independently
satisfy DMARC. The actual Authentication-Results, From/Return-Path or SES identity
status are still required. The operator supplied email body only. Do not replace
SPF blindly, publish duplicate SPF records or promise inbox delivery from a logo
fix. No SES or DNS settings were changed. References:
https://docs.aws.amazon.com/ses/latest/dg/send-email-authentication-dmarc.html
https://docs.aws.amazon.com/ses/latest/dg/email-authentication-methods.html

## Deployment and acceptance

1. Verify a recent recoverable database backup using the existing procedure.
2. Deploy API and dedicated worker from the same tested backend commit; wait for
   release reconciliation/alignment. Old-schema fallback keeps existing item
   threads usable until the additive migration runs.
3. Run `/app/across-migrate` once from the newly deployed API container. Check its
   successful exit and readiness. This applies 099/100; existing message ids stay
   intact. Production migration has not been run from this workspace.
4. Deploy `atlxpres-provider`; accept its centred app update. Publish the next
   native build separately when authorized; no APK/EAS/iOS build was started here.
5. Test an existing pair that has product and service history: one inbox entry,
   complete chronological older pages, old notification links, read/unread state,
   repeated-send deduplication and block enforcement. A different customer must
   not see that history.
6. Send a message each way to the updated Samsung PWA and Android APK while open,
   backgrounded and locked. Confirm sender image, app badge, sound and deep link.
   A push provider's accepted ticket does not establish phone display.
7. Register a synthetic test account through the normal flow and inspect the new
   verification email's inline logo and authentication results. Do not paste
   verification links or tokens into diagnostics. Resolve SES/DNS issues from
   those results, then retest placement; spam status is not yet resolved.
