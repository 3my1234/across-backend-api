# K3s preparation for three NEW application servers

29 October 2026. This branch prepares local/CI artifacts only. It does not install,
apply, purchase, move traffic, migrate Redis or alter either current VPS.

**Current topology decision supersedes the earlier reuse option:** retain the
existing main VPS for Coolify and the unrelated website; retain the existing
PostgreSQL VPS as an external database. Three planned NEW VPS form the K3s
embedded-etcd server/application cluster. Total five VPS if all are retained.
No current server is repurposed. Three voters tolerate one failed voter while
quorum remains; ingress, PostgreSQL and Redis failover are separate dependencies.
Actual new hardware, private IPs, node failure domains and region remain unknown.
No procurement or cost quote is approved by these artifacts.

## Working offline validation

Install the pinned `yaml@2.9.0` parser in an isolated tooling directory (or use the
existing mobile parser through NODE_PATH). Run from this backend worktree:

```powershell
$env:NODE_PATH="C:\Users\EMMA\Desktop\gouwu\mobile\node_modules"
node deploy/k3s/validate.cjs
node deploy/k3s/validate.test.cjs
node deploy/k3s/validate.cjs --release
```

The first two commands must pass. The last MUST fail: zero image digests, reserved
addresses, `.invalid` hosts and absent secret objects make the preparation
unreleasable. This is an offline contract checker, not Kubernetes admission or a
runtime deployment check. Do not weaken the placeholder guard to obtain a green
release flag. A concrete environment-specific package and separate policy review
are required before deployment; the current validator deliberately accepts only
the reviewed preparation image identities.

Manual-only `.github/workflows/k3s-preparation.yml` runs parser/policy regressions
and a separately environment-gated pinned kubeconform schema check. Configure
required reviewers on `k3s-preparation-review`: an environment name or nonempty
approval string alone is NOT human enforcement. The workflow has contents-read
permission, no kubeconfig/SSH/cloud secret and no apply/install/traffic step.
Kubernetes1.35 schema selection is a validation baseline, not a selected K3s
release. Actual K3s version, CNI/ingress versions and cluster admission validation
remain open. Local pinned kubeconform strict validation against Kubernetes1.35.0 passed all13 resources (12workload+1separateJob), zeroinvalid/errors/skipped. This did not contact acluster. No new workflow has been dispatched by this preparation task.

## Reviewed deployment contracts

`staging.yaml` contains12 resources. APIs use `/app/across-api` and worker uses
`/app/across-worker`, matching Dockerfiles. No image/tag built from another branch
is implied; publish exact digest from the qualified source only after approval.
Production/staging configuration validation remains active. Required runtime
secret must supply JWT_SECRET (32+bytes), verified PostgreSQL TLS (or explicitly
pinned encrypted peer), authenticated certificate-verified Redis TLS, and any
application credentials. No secret is committed. ConfigMap overrides disable
startup migrations, inline workers, dev login, bootstrap and mock payments.
Origins/trusted ingress CIDRs must be exact; replace them only after identifying
the actual proxy hops. Node private IP is not automatically the ingress sourceIP.

Health probe is process liveness; readiness checks dependencies. API shutdown's
3-second drain and65-second deadline require the90-second grace. EmptyDir/tmp is
bounded; real media/ffmpeg writable paths and memory need staging tests. Pods run
nonroot, read-only root, dropped capabilities and no service-account token.

APIs have required hostname anti-affinity, two initial replicas and HPA2..3.
Rollout is **maxSurge0/maxUnavailable1** so a three-node cluster at three replicas
does not deadlock waiting for an impossible fourth distinct host. The PDB keeps
one available for voluntary disruption; it does not protect involuntary node loss.
HPA requires metrics-server and CPU requests, does not create VPS nodes and is
not a qualified capacity claim. Review observed concurrency/admission/pool budgets
before enabling any autoscaling. MaxAPI pool12 + futureoneworker4 + migrator2 =18
connections BEFORE read pools, other roles, tools, old production consumers and
operational reserve. Read URL is disabled. The overlapping cutover budget must
include current Coolify consumers; do not simply add18 to the historical84/100.

## Worker and external dependency gates

Worker replicas stay ZERO. Actual push URLs are constants in
`internal/services/push_notifications.go` (`exp.host` send/getReceipts). Lab mocks
use a lab transport adapter; that is not a production worker transport switch.
SMTP uses configured mailtransport, browser-push targets stored subscriptions,
and other roles can invoke payments/other services. No real provider acceptance
or recipient delivery may be claimed from a stopped worker manifest. Qualify
entrypoint with isolated mocks/transport-egress design before activation.
Only email,push,push_receipts are listed; settlements/TownHall expiry/driver expiry
and other selected consumers need explicit ownership inventory, leases, health,
and scale design before calling this a full migration. Never autoscale singleton
financial consumers blindly. Current experimental receipt schema/cutover remains
a separately approved release action.

Default deny ingress/egress and explicit DNS/private DB/private Redis rules are
included. No production IPs are allowed by offline policy. Namespace mock routes
are placeholders, not a deployed mock. Google, billing, object storage, remote
OAuth/JWKS, real SMTP/Expo and other public egress are intentionally absent. A
functional production package must review those routes and test allow/deny paths;
opening0.0.0.0/0 is not the prepared solution. K3s CNI enforcement, hostNetwork,
Traefik labels/source-IP and WireGuard routing need actual cluster verification.
Existing Redis stays where it is: no migration, replacement or failover implied.

`server-config.example.yaml` is a template, not an installer. Unique NEW host/IP,
nonoverlappingCIDRs, encrypted network, private registration endpoint, protected
join token and etcd off-node snapshot/restore are unresolved. Bootstrap-first vs
join-other-server settings require explicit reviewed selection. No token value or
installer command is embedded. Kubeconfig permissions are0600 and secrets at
rest enabled in template. Reserve control-plane/etcd memory/SSDIO and verify all
three new nodes' placement; hostnames alone do not prove failure-domain separation.

`migration-job.example.yaml` remains separate, manual, backoff0 and bounded180s.
It must never be bundled with auto-startup; backup/schema compatibility/exact
source/userapproval are required before any execution. Networkpolicy currently
permits placeholderprivateDB for it but has no real endpoint/secret/image.

## Remaining acceptance and handover

1. Inventory new-node hardware/private-network/IP/CIDR/MTU/disklatency and independent
failure domains; select/pin K3s/CNI/Traefik/metrics versions and registration/ingress
HA endpoint. Existing mainhost/website and DB cannot be commandeered.
2. Obtain separate purchase/provision/install approval; no such commands run here.
Configure protected CI environment then validate schemas, real target API admission
and secret/configchecker with offline-only checks before pods start.
3. Isolated smoke: readiness/liveness, false dependency failure vs restart behavior,
HTTPdrain, uploads/tmp, rollingupdate/HPA, networkpolicies, secret rotation,
node/voter failure and recovery, etcdsnapshot+tokenrestore, externalDB/Redis loss.
4. Mockworker entrypoint roles/ownership/retries/fencing and receipt backlog recovery;
shared chat/Hall/driver/auth correctness across pods. Real traffic/providerdelivery
and production cutoff require subsequent explicit approval.
5. Freshbackup/offserverretrieval, rolloutrollback, oncall/alertreceipt and overlapDB
budget before a trafficchange. K3s assets do not convert isolated100 qualification
into production500 or millions capacity.

The user mentioned a PHP warning in the wider migration discussion. Exact warning,
source application, runtime, log and impact are not supplied in this workstream;
no invented fix or assertion that K3s resolves it is made. Retain unrelated website
on existing mainhost and obtain sanitized warning text before investigating it.

Official references checked29October:
- https://docs.k3s.io/datastore/ha-embedded
- https://docs.k3s.io/installation/requirements
- https://kubernetes.io/docs/tasks/run-application/configure-pdb/
