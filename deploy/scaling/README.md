# Two-replica pilot on the existing main server

Current status16 October2026: production HTTPS routing, replica failover and
customer/provider device checks passed. Initial setup instructions below are
retained for reproducibility; do not rerun the initial launcher on the accepted
installation. See [routing](ROUTING.md) and [managed releases](RELEASES.md).

This is a manual pilot, not production routing or autoscaling. It uses the same
production database/settings and must NOT receive a load test. No cloud, DNS,
Traefik, current API or worker changes are performed by the launcher.

Verified operator evidence, 16 October 2026: each host has 4 CPU/7.8 GiB RAM;
PostgreSQL max_connections=100, superuser_reserved_connections=3, 12 connected
clients and no lock waits in the supplied snapshot. Migration092 indexes exist.

Use a complete, pinned, reviewed checkout of this repository on the MAIN server
173.212.219.169. Do not run the launcher on the DB server. It requires Python3,
Docker and Compose with env_file format: raw support (Compose2.30+, verified main
host version5.3.0). The exact live API image commit is a required argument.

Read-only preflight:

```sh
python3 scripts/replica-pilot.py --expected-commit FULL_LIVE_API_COMMIT --max-connections 100
```

Start only after preflight passes:

```sh
python3 scripts/replica-pilot.py --expected-commit FULL_LIVE_API_COMMIT --max-connections 100 --execute
```

The launcher checks the current API and worker health, API revision/network,
shared Redis configuration and inline-worker separation. Existing API and worker
pool maxima (including any read pools) are counted, plus 10 connections for
other clients, a 20-connection reserve (including the three reserved PG slots),
and three pilot pools of8 (two running replicas plus one update surge).
With current API/worker primary pools20 each and no separate read pools, the
budget is94/100. The allowance for other clients must be revisited if other DB
consumers are added. These are pool ceilings, not observed concurrent usage.

Inherited settings are read internally from Docker and stored in a new root-only
directory /opt/atlxpres-scale-pilot-runtime/runtime.env. They are never displayed.
The image is pinned by local immutable image ID. Dollar signs and quotes in
credentials remain literal. Pilot overrides disable workers, migrations and the
extra read pool; each API has an eight-connection pool and64-request admission.
The pilot is capped at1.75 CPU and about1.125GiB combined container memory.

After startup, issue a small number of loopback readiness probes:

```sh
/opt/atlxpres-scale-pilot-runtime/manage.sh ps
for attempt in 1 2 3 4 5 6; do
  curl -fsS --max-time 5 -D - http://127.0.0.1:18080/api/v1/ready
  printf '\n'
done
```

The X-Atl-Pilot-Backend header identifies responding HAProxy backend slots.
Check both replica containers are healthy and more than one backend responds.
Readiness probes do not establish customer throughput or message correctness.
Do authenticated multi-replica and failure/load testing in isolated staging.

If startup fails, the live route is unchanged. Inspect or remove only the pilot:

```sh
/opt/atlxpres-scale-pilot-runtime/manage.sh ps
/opt/atlxpres-scale-pilot-runtime/manage.sh down
```

Do not print runtime.env or resolved Compose configuration, send them in chat,
commit them, or copy them off the host. The launcher deliberately will not reuse
or overwrite its runtime directory. Keep it until the pilot is removed; refresh
the pilot explicitly when production settings/image change. It is not managed
by Coolify's normal application deployments. Do not leave unmanaged stale pilot
containers indefinitely. Removing pilot containers does not erase the private
runtime file; use a checked, separately scoped cleanup when retiring the pilot.

Production cutover requires separate route inspection, authenticated behavior and
failure testing, a rollback procedure and an ongoing release process for replicas.
Same-host replicas do not provide host failover or additional physical hardware.
