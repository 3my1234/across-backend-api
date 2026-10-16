# Managed updates for the existing two API copies

This completes a release-safety step on current servers. It adds no API copies,
cloud resources or database capacity. It is not infrastructure autoscaling.

## Controller policy

`scripts/replica-release.py` is read-only without an operation flag. It checks the
actual running Coolify API and worker, their immutable commit-tagged images,
storage settings, existing replica health/settings and the connection budget.
Credentials stay inside Docker inspection and root-only runtime snapshots; they
are never printed or sent off-host. Pool ceilings include a deployment surge.

After explicit installation, a host systemd timer checks every15 seconds after
the previous check finishes. A healthy, unchanged deployment causes no container
or route updates. The controller waits while the API is unhealthy, while a
rolling deployment has multiple original containers, or while the worker hasn't
reached the matching healthy commit/storage configuration.

When a matching release/settings change is ready:

1. Verify original API dependency readiness and ownership of the dedicated route.
2. Remove only the owned replica route; confirm HTTPS uses the original API.
3. Save current API settings privately and pin its exact local image ID.
4. Refresh two replicas with eight connections each, no read pool, no migrations
   and no inline workers. Traffic remains on the original API during refresh.
5. Verify both replicas' image, settings and health match the current API. If
   another deployment occurred mid-refresh, stop instead of activating stale copies.
6. Reactivate the owned route and verify both replica slots over real HTTPS.

Refresh/verification failure keeps or restores the original API route and pauses
automation. It never rolls back the original API, database migrations, or customer
messages. Migrations must remain compatible with rolling API/worker deployments;
the controller does not make breaking migrations safe. It does not update the
worker itself: deploy API and worker from the same reviewed release in Coolify.

Manual route rollback disables the timer's activation marker. The controller
must not undo an operator's rollback. All route/release operations share a Linux
file lock, so they cannot overwrite each other's transition. Changed/unowned
route files are refused. While paused, the timer performs no release mutations.

## Install on the MAIN server

Use the reviewed pinned checkout that includes the controller and the updated
route tool. Preview (no changes):

```sh
cd /opt/atlxpres-replica-pilot
python3 scripts/replica-release.py --max-connections 100
```

Installation requires the accepted active route and currently aligned, healthy
original API, worker and two replicas:

```sh
python3 scripts/replica-release.py --install --max-connections 100
systemctl start atlxpres-replica-release.service
systemctl is-active atlxpres-replica-release.timer
journalctl -u atlxpres-replica-release.service --no-pager -n 12
```

Installation adopts the current deployment without restarting its containers.
It copies only reviewed script/Compose/HAProxy files into a private, content-
addressed controller directory outside the checkout. Git checkouts cannot silently
change the installed timer code. Future controller-code updates require another
reviewed installation. Unrelated systemd units are never overwritten.

The installed units are `atlxpres-replica-release.service` and `.timer`. Runtime
snapshots live under `/opt/atlxpres-scale-pilot-runtime/releases/`; credentials
are0600 in0700 directories. The management helper is updated only after healthy
replicas are ready. Earlier snapshots are deliberately retained for diagnosis;
checked retention/cleanup is separate work, not an automatic recursive deletion.

## Normal API releases

After installation and live verification, deploy the reviewed original API and
worker in Coolify as usual. The controller adopts the healthy matching release.
Watch the journal for `updated` and verify public readiness plus relevant device
flows. This automates consistency on one host; it does not establish a throughput
number or add physical compute. Failure can leave original-only service until an
operator resolves the issue and explicitly reinstalls/resumes the controller.

To pause automation and restore original-only routing:

```sh
python3 scripts/replica-release.py --pause
```

Manual route rollback remains supported:

```sh
python3 scripts/replica-route.py --rollback
```

To inspect a failed check, use the service journal above. Readiness monitoring
alone won't report a paused updater when the original API is still healthy;
updater failure notifications remain an operational integration to complete.
The controller's error output intentionally omits runtime values and raw Docker
stderr. Do not print private snapshots or resolved Compose config for debugging.

## Validation boundaries

Policy tests cover stable/no-op checks, release and secret changes, worker
version/storage mismatch, unhealthy original API, pool-budget rejection, refresh
failure, deployment races, manual pause/rollback, unexpected replicas, locking,
secret-safe previews and frozen installation. Existing Docker integration checks
cover literal env values, private alias ownership, dual-stack gateway transitions
and actual Traefik Docker/file-provider fallback/recovery. Controller adoption
and a controlled release still require operator verification on the real host.
