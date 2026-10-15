# Message receipts

Migration 091_message_receipts.sql adds per-message device acknowledgements and
account read-receipt preferences. No message body or location enters these tables.
Preferences belong to the authenticated user, shared across mobile/provider PWA;
admin identities have the same control for Support. Default is enabled.

Private product/service/shared chats and Support show:

| Indicator | Meaning |
| --- | --- |
| One gray tick | Message saved by the API; no recipient acknowledgement yet |
| Two gray ticks | Recipient app acknowledged receiving the message/its inbox preview |
| Two blue ticks | Recipient viewed it, and both parties permit read receipts |

Town Hall uses Delivered to / Seen by counts, not a claim that all ten providers
have read a message. Read counts respect the same reciprocal preferences.
Only outgoing messages show receipts. An Expo/FCM accepted push ticket is never
treated as proof of device delivery. Closed/backgrounded devices may remain at
one tick until their app next synchronizes; older client versions do not emit
these acknowledgements. This release does not add native background execution.
When the app is foregrounded on another page, a message notification can trigger
a bounded authenticated history fetch and delivery-only ACK. Fetches coalesce
per conversation with at most four concurrent jobs. The PWA service worker
forwards validated routing IDs only, never bodies/coordinates; authenticated
clients fetch the actual messages before acknowledging delivery.

Unread-count watermarks remain private and independent. Disabling read receipts
does not stop clearing the reader's unread counts or hide delivery. The server
suppresses storing read_at while that reader opts out and filters read status
if either sender or reader currently opts out. Read ACKs suppressed while off
are not retroactively published just because the preference is enabled later.
Reopening/reviewing the visible message can acknowledge it again.

API under /api/v1/messaging, or /api/v1/admin/messaging for authorized Support admins:

- GET/PATCH preferences: read_receipts_enabled boolean. The actor comes from auth.
- POST deliveries: messages[{channel,conversation_id,message_id}], max100. One
  indexed INSERT from membership-checked rows, delivery only, never marks read.
- POST receipts: channel,conversation_id,message_ids (1–100),read boolean.
  Incoming messages only; shared rooms require active membership, business chats
  require the buyer/active provider member, Support requires the owner/admin.
- GET receipts with channel,conversation_id,message_ids: returns own-message
  status and aggregate counts only. No peer preference flags or read timestamps.

There are no automatic chat-message retries. Duplicate receipt acknowledgements
are idempotent and delivery-only repeats avoid rewriting rows. Queries have a
five-second deadline. Status uses indexed message IDs in batches, does not
invalidate history ETags, create notifications, or delay sending/history rendering.
Private API responses use no-store. Receipt metadata is removed with its source
message; user/admin hard deletion removes preferences and recipient metadata.

Clients keep statuses in memory, outside persistent chat history. They refresh
every five seconds while an actual chat is mounted/visible and foregrounded.
Inbox previews use one batched delivery write, not a request per conversation.
Mobile uses FlatList viewability (50%,750ms); web uses IntersectionObserver and
pauses read acknowledgements under dialogs/hidden pages. Preference and actor
generations reject late status responses that could restore a hidden blue tick.
Unchanged statuses preserve UI state/DOM instead of redrawing message content.

Settings: mobile Account -> Settings -> Privacy and storage -> Read receipts;
provider/admin Settings -> Message privacy. Preferences save on the server and
show failure instead of pretending a failed toggle was saved.

Deploy updated API first with existing RUN_MIGRATIONS=true and worker false.
Apply 091 (and pending090) before clients. Then deploy admin and separate provider
resources; provider shell v15, Refresh app in installed PWA. User builds the new
mobile EAS binary. No new secret, phone/contact permission or worker loop needed.

Validation includes isolated PostgreSQL membership, reciprocal privacy, no
retroactive read disclosure, delivery-only batching, deduplication and cleanup;
mobile hook/indicator/settings tests including opt-out/account races; Chrome
ticks, inbox acknowledgements, visibility and Settings. Real Android/iOS/PWA
cross-device acceptance remains required; these checks are not a load benchmark.
