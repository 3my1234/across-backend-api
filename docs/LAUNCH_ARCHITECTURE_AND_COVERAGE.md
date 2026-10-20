# Launch architecture and feature coverage — 20 October 2026

## Evidence classification

Source inspected: backend main bd28d0d, portal main f5cb302, mobile main 07dafd6,
plus the changes in this PR. Production access from this workspace is limited to
public HTTP checks. Operator-provided server/SQL output is evidence at its stated
time, not continuous monitoring. No production credentials or data were copied.

Operator evidence on 19 October: main and database hosts each reported 4 CPUs,
7.8 GiB RAM. PostgreSQL max_connections=100 was verified on 16 October. The main
host later reported 6.4 GiB available memory, 11% disk usage and mostly idle CPU.
These snapshots do not explain the preceding photo/notification incident.

Latest operator routing evidence: two live HTTPS replica slots, commit 771fe3b,
pool8 each, release controller aligned and all readiness checks true. Later
source changes must not be assumed deployed without fresh operator evidence.

## Source-verified architecture

| Layer | Implementation | Capacity implication |
|---|---|---|
| Mobile | Expo/React Native with TypeScript | Native rendering needs device tests, not API load claims |
| Provider/admin | Plain JavaScript/CSS; Nginx containers; provider service worker | Browser permission, registration and OS delivery differ from native push |
| API | Go 1.22, Fiber/fasthttp; HTTP/JSON | Admission bounds application concurrency; this is not autoscaling |
| Identity | Application HMAC-signed bearer session; database session_version/active lookup; bcrypt password login; Privy verification for federated sign-in | Active sessions create database work; JWT_SECRET naming does not make this a standard JWT |
| Chat | HTTP polling, bounded history, ETags; mobile shared chat 20s, product chat 15s; receipt polling separately | There are no persistent WebSocket chat clients to count; quantify pollers and request rates |
| Database | PostgreSQL via pgxpool; default max20, replicas max8 verified by operator; optional separate read pool | Connection budgets must include workers, original API, surge and diagnostic clients |
| Cache/limits | Redis, five-second public catalogue cache, per-process miss coalescing; private/authenticated bypass; Redis fixed-window limits | Synthetic distinct forwarded IPs do not qualify shared-hotspot behavior or forwarded-header trust |
| Routing | Coolify/Traefik to HAProxy, two same-host HTTP copies with original API fallback; matching-worker release controller | Replica survival is not main-host survival; no automatic new-host provisioning |
| Media | Presigned direct S3 PUT; private chat signed GET; public media may proxy via API unless CDN configured | Actual upload/retrieval and public avatar size must be measured separately |
| Video | Synchronous MP4 download/ffprobe/verification/publish with two verification slots per API process | This is not an asynchronous video processing queue; avoid assuming worker scaling solves video CPU/bandwidth |
| Jobs | Dedicated Go worker; durable PostgreSQL email, native/web push and settlement jobs; leases and retries | Existing HTTP lab does not demonstrate backlog drain or delivery to phones |
| Commerce | Customer/provider contact marketplace; product checkout deliberately rejects; paid provider subscription flows remain | Do not simulate nonexistent launch checkout or label it successful |

## Coverage matrix

“New” means runnable checks added by this PR; execution evidence is in the results
report. Existing test coverage is correctness coverage, not proof at 12,000 users.

| Feature | Actual route/client | Coverage / remaining boundary |
|---|---|---|
| Signup/password validation | POST /auth/signup | New lifecycle checks for buyer/product/service/mixed actors; not signup spike qualification |
| Email verification | GET /auth/verify-email | New lifecycle uses real handler and a synthetic DB token; no outbound email |
| Login/unverified rejection | POST /auth/login | New lifecycle and per-VU login; bcrypt exercised |
| Active/expired sessions | GET /auth/session | New identity and 401 checks; signed session version correctness also has existing tests |
| Logout/refresh | Client logout; no server logout or refresh route found | No invented endpoint; session invalidation needs distinct correctness tests |
| Product browse/search/filter | GET /products (search, stock_scope, bounded pages) | New mixed-session reads, varied terms and local filter; existing lab catalogue |
| Service/property browse | GET /marketplace/listings; detail by id | New session reads/details; fixtures are hotels, property subtype fixture still needed |
| Pagination | Products/listings/history use cursors | Existing lab bounds pages; next-page traversal still needs dedicated workload |
| Public profile/avatars | Provider identity embedded in product/service payload; client displays avatar/banner | Existing browser layout tests; live image transfer and thumbnail performance unqualified |
| Favourites | No server favourites route or current launch feature found | Unavailable; do not count as tested |
| Provider roles/onboarding | POST /providers/onboarding, GET /providers/me | New lifecycle checks three supported provider types; scaled seeded providers remain mixed |
| User profile update | PUT /profile | New lifecycle checks persisted update response; not competing profile-write stress |
| Provider profile update | PATCH /providers/me | Existing API; not yet in session workload |
| Service/product create/edit/submit/archive | /providers/me/listings and /products routes | Existing provider policy integration tests; not yet a full session lifecycle under traffic |
| Admin moderation | /admin/providers, /provider-listings, /merchant-products | Existing correctness tests; session workload currently reads business overview only |
| Image presign/PUT/GET | /marketplace/chat-images/presign; S3 URLs | Source inspected; isolated real-storage transport is missing; upload-heavy mode is explicitly blocked |
| Video complete/limits | POST /providers/me/videos/complete | Existing mocked storage/ffprobe policy tests; no media throughput claim |
| Inbox/unread | GET /chat/inbox | New session workload; separate unread correctness tests exist |
| Marketplace send/history | /marketplace/conversations and /providers/me/conversations | New bidirectional activity, retry with same client id, recipient polling observations, DB reconciliation |
| Delivery/read | POST/GET /messaging/receipts | New inbound read/delivery acknowledgement; observing sender status still requires fuller flow coverage |
| Shared room history/send | /chat/conversations/:id/messages | New session workload; outsider access and full-room membership covered in existing integration tests |
| Town Hall create/apply/approve/offer | /chat/town-hall routes | Existing Go lab and integration tests; new k6 uses admitted room traffic, not admission lifecycle load |
| Busy Town Hall | One owner + ten admitted providers per hot room | Seeded and read/written by provider sessions; observed fan-out/poll deliveries are not phone pushes |
| Support | /support/tickets/:id/messages and /reply | Existing lab reads/replies; new session workload reads |
| Orders/product payments | Legacy routes reject launch checkout | Existing contact-only guards; no sandbox product charge to simulate |
| Subscriptions/renewal/callbacks | /providers/me/subscription-*, /payments/flutterwave/webhook | Existing mocked integration coverage; test-account sandbox end-to-end run not configured |
| Admin analytics | GET /admin/business-overview | New mixed workload interleaves small admin reads; frontend rendering remains browser-test scope |
| External email/SMS/native/browser push | Worker/vendor transports | Suppressed in isolated lab; internal queue row counts are recorded, drain capacity/display not tested |
| Backup/restore | Separate DB runbook | Existing procedures; a fresh restore/reconciliation drill remains required |

## Initial release recommendation

Not ready to claim the requested 12,000 simultaneous-session workload. Until
device media/push issues, representative load and recovery gates have evidence,
the appropriate path is controlled internal testing. A passed four-session smoke
qualifies the harness and selected flows, not a public capacity promise.

Prioritize: (1) verify deployed fixes and affected phone paths, (2) isolate media
and worker delivery measurements, (3) deploy staging on the production-like split
with a separate generator, (4) advance session stages only after healthy evidence,
(5) add host redundancy and dependency recovery according to measured limits.
