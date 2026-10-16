# Reviewed route cutover and rollback

The replica pilot is a manual two-copy deployment, not autoscaling. Both copies
and the gateway have been tested by the operator for readiness distribution and
stop-one/restart failover. Authenticated device acceptance and staging capacity
measurement remain required. No million-user capacity has been established.

The operator's proxy inspection confirms:
- Production API and coolify-proxy use the coolify Docker network.
- Proxy mounts /data/coolify/proxy at /traefik.
- Existing HTTPS router uses api.atlxpres.com, gzip and letsencrypt.
- Original API is kept running as a fallback and rollback path.

`scripts/replica-route.py` is read-only by default. It verifies exact observed
router rule, middleware/TLS configuration, explicit file-provider watch and HTTPS
listener, expected directory mount, healthy identical API image IDs and no
existing owned route. If configuration differs, it stops; do not bypass guards.

The script uses one dedicated watched file:
/data/coolify/proxy/dynamic/atlxpres-replica-route.yaml. It never edits or replaces
Coolify files or existing routers. The higher-priority router references the
gateway service, with the original Docker-provider API service as fallback if
the gateway readiness health check fails. It does not configure status-based
failover or retry middleware: uncertain writes must not be replayed.

## Operator sequence on the MAIN server

Update the existing /opt/atlxpres-replica-pilot checkout to the exact reviewed CI
revision. Do not run the initial pilot launcher again. Preview:

```sh
cd /opt/atlxpres-replica-pilot
python3 scripts/replica-route.py
```

Review the printed host, HTTPS entry point, certificate resolver, gzip@docker,
service URLs, original @docker fallback and sole new file path. Then activate:

```sh
python3 scripts/replica-route.py --execute
```

Activation recreates only the gateway so its shared-network membership is
persistent in Compose. It does not recreate APIs/workers or modify DNS/proxy
static configuration. It atomically publishes the dedicated route and tests
local HTTPS with real TLS/SNI against127.0.0.1. If two healthy replica slots are
not observed within a bounded verification period, it removes its own file to
restore original routing. Interrupts also trigger removal. SIGKILL/host failure
cannot run automatic rollback; the manual command remains available.

Root-only route-state.json records the dedicated path and content SHA256.
Rollback refuses to delete a symlink, an unowned path or a file changed by
another operator. It does not stop APIs/gateway or remove the private runtime:

```sh
python3 /opt/atlxpres-replica-pilot/scripts/replica-route.py --rollback
curl -fsS --max-time 8 https://api.atlxpres.com/api/v1/ready
```

Traefik watches files asynchronously; allow its reload interval after removal.
Existing HTTP-to-HTTPS redirect and unrelated domains remain on original routers.
Health-based fallback may take the configured10-second check interval to detect
a failure. It is not a guarantee of zero failed requests during abrupt outages.

## Mandatory device checks and release procedure

Immediately after cutover, use test customer/provider accounts to check login,
product/service chat sends in both directions, images and location pins, receipts,
Support, Town Hall browsing/admission/messages and upload signing. Verify public
HTTPS responses as well as the local check. Roll back on failed acceptance.
Do not send load tests at this shared production database.

The pilot is NOT updated by normal Coolify deployments alone. The separately
installed [release controller](RELEASES.md) can reconcile healthy API/worker
releases after its host installation and verification. Until that managed replica
release process is installed, rollback this route BEFORE any API code, signing
secret or runtime-setting deployment. Deploy the original Coolify API/worker;
then refresh the pilot explicitly, verify identical image/settings and repeat
acceptance before reactivation. Do not leave customer traffic on stale manually
managed copies. Keep the checkout and root-only runtime until routing is removed.
Changing API code and refreshing replicas must be treated as one coordinated
release; this limitation remains outstanding work for automatic expansion.
