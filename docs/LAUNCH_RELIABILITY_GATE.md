# Launch reliability gate: 19 October 2026

## Current evidence

The operator verified original API/worker alignment at 771fe3b, both replica
slots over HTTPS and readiness with pool maximum 8. Both replicas share the
main Contabo host. The main host snapshot had 6.4 GiB available memory, 11%
disk usage and 88–95% CPU idle in four interval samples. This does not establish
resource usage at the time of the reported incident.

A customer APK reported a 12-second failure while requesting chat image upload
authorization. That request precedes the direct S3 PUT. Ordinary messages still
arrived on the provider's Samsung PWA, but phone notifications did not appear,
and the customer's avatar loaded slowly. Both phones shared a hotspot.
The hotspot is context, not a demonstrated cause.

Database aggregates subsequently showed two active browser push registrations,
ten sends marked `sent`, and no failed/retry jobs over 48 hours. `sent` means
the browser push service accepted a send; it does not prove the affected phone
was registered or displayed it. APK push uses a separate native delivery path.

## Confirmed code weaknesses addressed

- PWA push registration did not retry on online/resume, could wait indefinitely
  for service worker readiness, and did not explain default/blocked permission.
  Retry is bounded and throttled; permission prompts remain user initiated.
- Foreground provider sound created a new AudioContext during polling without
  resuming audio after a user gesture. A reusable context is now unlocked by
  interaction. Phone/browser notification sound still depends on OS settings.
- Unless ASSETS_CDN_BASE is set, public image URLs pass through the API.
  Upstream header and image body waits are now bounded. Images exceeding 20 MiB
  or incomplete reads fail before successful/cacheable headers are returned.
  Private chat and verification media are excluded from the public image route.

These are fixes for observable code defects, not a retrospective root-cause
finding for the operator's incident. Full-resolution avatars and media delivery
latency are not resolved by imposing an upstream deadline.

## Production validation before store submission

1. Deploy the tested API and worker at matching commits; verify the updater
   reports `updated`/`aligned`, both HTTPS replica slots and dependency readiness.
2. Deploy both web resources and accept the PWA app refresh on test phones.
3. On the affected Samsung, record browser/version, permission state, installed
   versus browser-tab launch, and notification status. Send a customer message
   with the PWA open, backgrounded and screen locked. Record whether each appears
   in phone notifications as well as the application bell; test sound separately.
4. Upload three chat photos individually. Confirm their appearance on the peer's
   phone and reopen them. Repeat on hotspot, an independent mobile connection
   and Wi-Fi. Record exact time/timezone and failure stage. Never capture signed
   upload URLs, session headers, private messages or subscription endpoints.
5. Correlate failures with proxy/API timings, request IDs, pool waits,
   worker/browser-push queue state, deployment and OOM/restart events. A green
   readiness response after the event is insufficient.

## Recommended topology and expansion order

Retain the separate PostgreSQL host and existing data. Do not restart the project
or provision one server per portal. The static portals can remain co-hosted.

1. Put public images/assets behind a CDN backed by S3. Audit bucket policies,
   existing public URL mapping and cache behavior before switching ASSETS_CDN_BASE.
   Exclude private chat and driver/verification documents. Generate bounded
   avatar/card thumbnails rather than serving original-size photos in tiny UI.
   This removes public image bandwidth from the API host; it needs separate
   configuration and validation and has not been deployed by these code changes.
2. Add external API latency/error monitoring, per-stage upload measurements,
   browser-push connection diagnostics and worker queue-age alerts. Observe from
   a Nigerian network as well as the hosting region. Alert on release-controller
   failure/version mismatch even while the original API is healthy.
3. Add a second application host in an appropriate nearby region and a load
   balancer that survives either application host failing. Restrict database and
   Redis connectivity over an authenticated private network. Keep workers
   separate from HTTP replicas. Do not expose PostgreSQL or Redis to the world.
4. Review shared Redis availability, PostgreSQL backups/restores and standby
   failover. Additional API replicas do not make either shared dependency highly
   available. Size connection pools against the database's total budget; assess
   PgBouncer before expanding beyond that budget, not by blindly increasing
   max_connections.
5. Load-test the proposed topology on an isolated production-like deployment:
   browse, authenticate, messages both ways, busy Town Hall fan-out, photo upload
   authorization, public avatar/card downloads and real notification workers.
   Stage concurrent sessions and request rates independently and test recovery
   and host loss. Preserve confirmed writes; no private production load replay.

## Capacity claims

10,000 registered users, daily active users and simultaneous sessions describe
different workloads. Establish a measured normal-service envelope by request mix
and concurrency. Existing lab burst tests intentionally rejected many requests;
they prove overload protection/recovery rather than normal service for all
offered traffic. Existing lab workers were disabled, so notification backlog
drain and actual mobile media behavior remain unqualified.

Release evidence must include successful-request p95/p99 per operation,
unexpected errors/timeouts, rejected requests, database waits, worker backlog age,
media bytes/latency and confirmed/stored writes. The initial lab p95 goal is
500 ms for API operations; define separate device/network media targets and
measure them rather than inferring them from API timings.

No new paid service or server is created by this document. Infrastructure
selection, costs and production cutover remain concrete deployment work.

References: Chrome audio policy https://developer.chrome.com/blog/web-audio-autoplay
and browser subscription lifecycle https://web.dev/articles/push-notifications-subscribing-a-user.
