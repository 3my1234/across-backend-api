# Core-feature launch audit — 2026-11-06

This checks release source and the current mobile checkout, not the deployed
production image or store binaries. External reviewers' traffic estimates are
hypotheses, not a measured safe user limit.

| Area | Confirmed implementation | Remaining risk |
| --- | --- | --- |
| Town Hall | Ten active providers per room, enforced by handler and database admission trigger; room lock serializes closure/membership/writes; retries and notification writes stay transactional. | Hot-room lock and pool waits remain measured problems. A fifty-admitted-provider room is not the current product contract. Do not remove locking or silently raise the cap. |
| Drivers | Mobile foreground heartbeat normally every 30 seconds; Android minimized location requests a 30-second interval. Publication is serialized and suppresses duplicates within 13 seconds. Server rejects writes inside ten seconds and expires presence after 120 seconds. | Location publication writes PostgreSQL, not Redis. Phone scheduling is not guaranteed. Delayed/failed updates must expire; steady-state throughput is unqualified. |
| Nearby discovery | PostgreSQL GiST point index and bounding-box filtering before precise distance/eligibility checks; at most 25 results; five-second handler context deadline. | A spatial index is not proof that the full query or concurrent presence updates meet latency targets. |
| Mobile chats | Shared conversation/inbox polling every 20 seconds while app active; product chat every 15 seconds; Town Hall discovery/pending-detail refresh every ten seconds. No WebSocket rewrite proposed. | These are checkout-specific intervals, not every client or deployed binary. Many simultaneous polling clients still generate load. |
| Mobile retries | Driver loop avoids overlapping requests; transient failure delays increase 10/20/30 seconds and cap at 30; authorization/access failure stops availability. Provider request helper does not blindly retry every write. | Driver retry is not exponential with jitter. Do not claim a complete mobile retry-storm audit from these helpers alone. |
| Media | Provider media and chat photos request signed upload URLs and PUT file bytes directly to storage. Signed uploads omit application credentials. | API still signs/validates/completes media, and device compression/video completion cost are not qualified by a JSON load test. |
| Overload | API request admission defaults to 128 in-flight per process with retryable 503; shared Redis-backed auth/community rate limits exist. | Effective production settings and Cloudflare rules were not queried. Application pool code applies explicit SQL timeouts to the migration pool; this does not establish runtime Town Hall lock timeout settings. |

Source references:

- `internal/controllers/community_town_hall_controller.go`, `community_controller.go`,
  `migrations/080_shared_chat_town_hall.sql`, `internal/routes/routes.go`.
- `internal/controllers/driver_availability.go`, `migrations/089_driver_availability.sql`.
- `internal/middleware/admission.go`, `internal/db/db.go`.
- Mobile `components/providerDriverPresence.ts`, `driverLocationTask.ts`,
  `driverPresenceSession.ts`, `CommunityScreen.tsx`, `ProductSellerChat.tsx`,
  `CommunityTownHall.tsx`, `nativeProviderApi.ts`, `NativeProviderCatalog.tsx`,
  `chatImages.ts`.

Existing mobile driver-session and background-location tests passed when run
from the mobile project directory. They verify lease/account scoping, sign-out,
serialization, stale location rejection and foreground service behavior with
mocks. They do not establish behavior of a released Android/iOS binary on devices.

## Mixed checkpoint

Dedicated test branch `experiment/launch-mixed-checkpoint`, source `1572457`, uses
the existing qualified generator bytes and original stage policy at 20 sessions
for 180 seconds. API is pinned to `395ef241b9a35be9712cb6fc7d107bfe8be85f38`,
whose PostgreSQL 16 regression passed in `42203503855`; both running API executable
hashes must match the built artifact. Full 500-pair, large-busy fixture, real
PostgreSQL 16/Redis, ordinary chats, hot Town Hall activity, presence/offline and
nearby search remain in the existing mixed workload. No fixture reduction or
latency/error threshold relaxation. Gateway captures X-Cache; per-operation k6
results, resource/lock samples and independent write reconciliation are retained.

Run: https://github.com/3my1234/across-backend-api/actions/runs/42210541636

This is a free disposable GitHub runner, not either VPS. Resource quotas do not
guarantee independent CPU cores on the runner. Even a pass is not 500-session
VPS qualification. It is bounded release-candidate evidence to decide the next
action. No production deployment, migration, SSH password or purchase is involved.

## Release decision requirements

No broad-launch sign-off is established by the earlier partial tests. The pending
checkpoint must report feature-specific results and separate latency, generator,
fixture and write-integrity failures. Do not label a failed performance gate as
data corruption, or passing acknowledged writes as a full capacity pass.

Current billing/payment lifecycle, SES production sending readiness/quotas, actual
device push delivery, store binary identity, backup restoration and deployment
rollback still require their own existing release evidence. This audit did not
access the live accounts or verify these gates. Pacing announcements can reduce
a synchronized burst but does not prove any numerical user capacity.
