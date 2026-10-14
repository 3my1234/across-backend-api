# Shared messaging and Town Hall specification

Status: implementation in progress, 14 October 2026. No new social/Town Hall
release has been deployed yet. The existing contact-only marketplace release
remains the production baseline.

Username-first scope: the user prefers usernames after confirming international
Privy phone verification carries charges. Use unique usernames, shared invitations
and accepted friends. Phone OTP and address-book matching are deferred; no contacts
permission or paid SMS service is required for this release. Connection-based
promotions use accepted friend edges with separate opt-in.

## One messaging experience

Use one familiar chat interface and inbox for private DMs and product/service
enquiries. A listing or Town Hall request provides context; it must not be a
prerequisite for a new private DM. Personal identity and provider ownership refer
to the same underlying user, while business-team access must not grant access to
that user's personal DMs.

Model conversations with explicit participants, roles, per-member read markers,
message attachments and optional business/listing/request context. Existing
buyer/provider history and permissions must be preserved. The current two read
columns and buyer/provider sender types cannot represent ten separate providers.

## Confirmed Town Hall behaviour

- A small movable Town Hall icon opens a directory of buyer requests for products
  or services and shows relevant activity. Keep it clear of composers, keyboards,
  image viewers and navigation.
- A buyer creates a request with description, location, quantity/unit when relevant,
  currency and target budget. Example: 50 bags of rice at NGN48,500 per bag.
- Nonparticipants can view the request and offer previews. They cannot enter the
  private bargaining conversation, read its chat or send replies until admitted.
- Verified Free providers can browse/open Town Hall and see those previews. They
  cannot request entry or participate until subscribed to at least Bronze/NGN500.
- Verified providers with an active confirmed paid subscription can request entry.
  A request is not admission: the buyer must approve it.
- Maximum ten admitted providers at once, plus the buyer. A full room displays
  Full. Kicking one provider opens one place; kicking two opens two places.
- Newly available places still require buyer approval. Providers cannot take a
  reopened place automatically or bypass approval by rejoining after removal.
- Providers can participate in multiple independent Town Hall requests.
- Buyer can choose an offer and continue privately with its provider. Carry the
  request and chosen offer into the DM, not the full competing negotiation.
- Buyer can end the request, removing active Town Hall participants. The chosen
  private DM and the providers' other rooms remain independent.
- Products remain contact-only. Town Hall does not restore product checkout,
  escrow, Flutterwave settlement accounts or automatic financial transfers.

## Preview and bargaining boundaries

Offer previews are deliberately structured public fields: provider public identity,
quantity/unit, currency, unit price/total and offer terms intended for the preview.
Private bargaining text, private attachments, participant read state and personal
contact details must not be embedded in preview responses and merely hidden by UI.
Define these preview fields consistently on mobile and provider PWA.

| Visitor state | Request/offer previews | Request entry | Private bargaining |
| --- | --- | --- | --- |
| Verified Free provider | Yes | Upgrade required | No |
| Verified active-paid outsider; space available | Yes | Request buyer approval | No |
| Pending applicant | Yes | Waiting for approval | No |
| Active-paid outsider; ten providers admitted | Yes | Full | No |
| Admitted provider | Yes | Already admitted | Read/reply |
| Removed provider | Yes while previews remain open | Fresh buyer approval required | Access revoked |
| Buyer/request owner | Yes | Approve/remove providers | Read/reply/close |

Pending applicants do not occupy admitted seats. Closed requests and their offer
previews disappear from public browsing; the buyer retains the record.

## Server-side enforcement

Approval must be one transaction: lock the request, verify it is open, authorize
its buyer, verify the provider's approved/active identity and current confirmed
paid subscription, check the admitted-provider count, then admit once. This must
prevent two concurrent approvals for the tenth place from admitting eleven.

Only the owner may approve, remove or close. Authentication, membership, removals,
blocking and room state must be checked on every private read/write and attachment
authorization. Pending applicants and Free providers cannot bypass admission using
a message endpoint or an old client. Count provider identities consistently, so
multiple business staff cannot accidentally occupy multiple seats for one business;
the staff participation policy still needs a final definition.

Removal/closure invalidates membership caches and live subscriptions. Preserve the
historical record for appropriate authorized access; removal does not delete or
alter unrelated private chats. Creating/reusing the selected DM and optional room
closure must be safe to retry without creating duplicate DMs.

Reuse existing notification delivery, photo viewing/upload protections, pagination,
idempotent sends and keyboard handling. Fan out room updates to authorized viewers;
do not broadcast private negotiations through global activity events. Use bounded
queries, indexed membership/history and Redis event distribution across API replicas.

## Implementation sequence

1. Shared conversation participants/permissions and one inbox, retaining existing
   product/service chats and supporting direct conversations without a listing.
2. Username profiles, invitations and accepted friend connections, with blocking
   and reporting. Existing account authentication remains responsible for login;
   a username is a public identifier, not proof of phone ownership or verification.
3. Personal statuses with audience controls and clearly labelled paid-provider
   promotions. Extend promotion reach through at most three accepted friend edges,
   with separate opt-in, relevance, frequency limits and blocking enforcement.
4. Town Hall request/offer previews, paid admission requests, buyer approval/removal,
   concurrent ten-seat enforcement and safe DM handoff. Add mobile/PWA interfaces,
   the movable activity icon and targeted notifications, with privacy and
   concurrent-admission tests.

The user confirmed the original social phases should precede Town Hall. Phone OTP
and address-book matching are deferred in favour of usernames. Do not request
contacts permission or purchase SMS services for the username-first release.

## Decisions still open

- Confirmed: expiry immediately stops Town Hall messages/offers until renewal.
- Confirmed: closed requests disappear publicly; the buyer retains the record.
- Request expiry, moderation/report handling and staff participation.
- A future contact-verification service, if phone discovery is later requested.

Do not silently impose these policies or treat next-phase planning as confirmation
that the features are implemented or live.
