# Provider branding, nearby drivers and location pins

Providers can upload/replace a public profile photo and banner in provider
Settings → Public profile. This works in the PWA and embedded native provider
workspace. Existing profile photos remain intact. Public identity responses
carry the photo/banner through product and service cards/details and customer
conversations. Branding is separate from private verification uploads.

Independent driver, private driver/chauffeur, intercity and airport driver
categories are available. Searches for Uber, Bolt, taxi, drop and lift resolve
to driver services; these are search aliases, with no claimed affiliation.

## Availability

An owner/manager selects an approved driver listing on the provider overview,
then explicitly taps Available nearby. Each heartbeat requires active approved
provider documents and that provider's approved driver/chauffeur/taxi listing.
Free verified providers may use their one approved listing. Town Hall admission
continues to require an active paid subscription.

The foreground PWA/native workspace refreshes location every 30 seconds, with
fresh device permission and a position no older than 60 seconds/accuracy <=1 km.
The server lease lasts 120 seconds. Going offline, hiding the PWA or pausing the
native workspace stops updates and attempts an authenticated deletion. After an
interruption the driver must explicitly go online again. A failed network stop
cannot keep a driver visible beyond the lease. Native background tracking is not
implemented or requested.

Services → Drivers nearby uses the customer's current foreground location and
an authenticated, uncached 10 km search. Results are capped at 25, paid first
then distance, and return distance rounded to 0.5 km. Exact driver coordinates
are omitted. A GiST point index bounds the distance query, including date-line
boxes. Availability is checked on every read. No travel history is stored;
expired latest-position rows are purged in batches by the worker every minute.

## Private static pins

Chat composers support an explicit location-pin attachment, with preview and
removal before Send. This works for legacy product/service chats, shared DMs and
admitted Town Hall bargaining rooms. Town Hall's public request/offer previews
do not include these pins. The permission explanation identifies who can see
the pin. Coordinates are validated server-side, stored as structured JSON,
included in idempotency checks, and protected by existing membership/blocking/
subscription rules. They are static snapshots, not timed live tracking.

Pins are retained as messages and may be included in the existing encrypted
device chat cache. A map link opens Google Maps on deliberate tap; no map API key
or continuous embedded map requests are introduced.

## Deployment and checks

Deploy API with already-authorized RUN_MIGRATIONS=true; apply migrations
087_provider_profile_media.sql, 088_chat_location_pins.sql and
089_driver_availability.sql before worker/clients. API readiness checks the
community schema, including pins and presence. Deploy worker with migrations
false; readiness adds driver_expiry. Deploy both admin and standalone provider
resources; provider uses Dockerfile.provider on port 8080. PWA shell v13,
release string 20261015-drivers; installed users select Refresh app. The user
runs the new EAS build. No extra keys or SMS provider are required.

Acceptance: upload both images; confirm cards/details/chat avatar and banner;
remove one without removing the other. Approve a driver listing, go online,
search nearby from a second account, message multiple drivers, and exchange
pins. Background the driver and confirm disappearance within two minutes.
Verify unapproved/non-driver listings are denied, revocation hides existing
presence, and expired positions are purged. Test pins in both old listing chats
and a Town Hall-to-DM conversation, denied location permission, slow GPS,
retries, keyboard layout and PWA reload. Real Android/iOS permission and phone
GPS acceptance remain device checks; no production capacity guarantee is made.
