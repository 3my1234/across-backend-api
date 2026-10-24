# Selectable background consumer roles

Prepared candidate; no production topology/configuration change. This branch is
based on PR66 head be1336c and does not merge or modify that PR's source branch.

WORKER_ROLES is `all` by default (blank retains the old behavior). The nine
existing consumers still start; the integrated billing candidate adds an opt-in
play_billing loop that is idle when Play billing is disabled. An explicit comma-separated selection starts
only those existing implementations. Invalid/duplicate names reject before
dependency startup in the worker preflight; values are not echoed in errors.
Only selected loops participate in heartbeat health and watchdog decisions.

Supported names: web_push, email, push, push_receipts, auto_confirm,
batch_closure, settlements, town_hall_expiry, driver_expiry, play_billing.

Possible separately reviewed deployment grouping:

| Process | Selection |
|---|---|
| Notifications | push,push_receipts,web_push |
| Email | email |
| Maintenance | auto_confirm,batch_closure,settlements,town_hall_expiry,driver_expiry |

These groups are not deployed by this change. Selection itself does not isolate
memory/CPU or create a new database pool: separate deployments with explicit
resource limits and total connection budgeting do that. Preserve durable queue
claims, leases/idempotency and existing loop implementations. No migrations,
new queues or business-processing algorithm changes are introduced.

Do not set a subset on the current sole production worker and omit other jobs.
The current release controller assumes one original worker resource; multiple
role deployments require an explicit version/health/coverage inventory, updated
coordinated release gating and a reviewed aggregate DB budget before rollout.
Roll back only through a coordinated topology change that maintains required
consumer coverage. The default all-role behavior is retained to permit the
existing single-worker release model while that design is qualified.

Validation covers default/backward compatibility, invalid selection failing
before any consumer starts, exact selected execution, graceful cancellation,
role/monitor inventory consistency, ignored disabled-loop heartbeats and selected
loop stall detection. Real job ownership/retry/duplication and mixed-release
behavior remain coordinated synthetic staging requirements.
