# K3s implementation preparation checkpoint ? 29 October 2026

Branch `experiment/k3s-preparation`; isolated worktree `.worktrees/k3s-readiness`;
base `eb9ffd1` (current experimental launch candidate with batched chat notification
writes). This branch changes deployment preparation only, no application SQL or
runtime code. Existing main VPS retains Coolify and unrelated website; existing
DB VPS remains external PostgreSQL; THREE NEW VPS form embedded-etcd K3s. Five
hosts total while all retained. This supersedes the28October assessment's reuse
option. No purchase, installation, routing, DNS/firewall, Redis migration,
production command, secret provisioning or workflow dispatch occurred.

Implemented files:
- `deploy/k3s/staging.yaml`: explicit staging safety settings, hard API host
separation, bounded pool/HPA, three-node-safe rollout (zero surge/one unavailable),
nonroot/tokenless/read-only pods, process/dependency probes,90sdrain allowance,
private Service/TLS ingress placeholders/default-deny policies. Worker remains0.
- `deploy/k3s/server-config.example.yaml`: new-node-only private/encrypted-CNI,
secrets-encryption/kubeconfig0600/snapshot template; no installer or tokens.
- `deploy/k3s/validate.cjs` and `validate.test.cjs`: offline invariant checker and
ten mutation/contract/source tests; rejects startup migrations/liveworker/unboundedAPI/
impossiblefourthsurge/productionDB/tokenmount/wrongbinary/dependencyliveness.
`--release` intentionally fails unresolved reserved endpoints/digests/identities.
- `.github/workflows/k3s-preparation.yml`: manual-only local/CI validation path,
read-only repository permissions/no cluster credentials. Separate schema job uses
reviewenvironment `k3s-preparation-review`; its required reviewers MUST be configured
outside this source before claiming enforced humanapproval. No apply/deploy step.
- `deploy/k3s/README.md`: source-specific dependencies, node/ingress/quorum/DBbudget,
mockworkerstopgate, exact validation, operationalacceptance and remaininghandover.

Localtests: YAML parser2.9.0; twelve workload resources parsed and policyPASS;
ten regressionsPASS; release gateEXPECTEDFAIL. Standard node--test subprocess was
blocked by Windows sandboxEPERM; invoking the same testfile directly executed all
node:test cases successfully. Kubernetes schema validation result recorded below
when completed; offline policy checks are not live API admission/runtime tests.

Actual source audit: API `/app/across-api`, worker `/app/across-worker`, migrate
`/app/across-migrate`; runtime config requires verifiedDB TLS/pinned encryptedpeer,
authenticatedverifiedRedisTLS,32byteJWT and exacttrustedproxyCIDRs. API drains3s
and permits65sshutdown. Push send/receipt URLs are hardcodedExpo constants; browser
push has stored external subscription destinations; SMTP configured transport.
Lab mock adapter is not automatically realworker transport. Ten worker roles exist:
web_push,email,push,push_receipts,auto_confirm,batch_closure,settlements,
town_hall_expiry,driver_expiry,play_billing. Prepared stopped subset is not full
platform migration. Receipt migration108 and old/new worker ownership remain a
separate release/cutover gate, not executed by any K3s artifact.

BoundedfutureDB estimate: up to3APIs?4pool=12 +oneworker4+migrator2=18 before old
Coolify consumers/otherroles/tools/reserve. Current readURLdisabled. No automatic
pool increases or max_connections change. Actual cluster resource sizing,
new-nodefailure domains/privateIPs/MTU/storage performance, nonoverlappingCIDRs,
pinnedK3s/CNI/Traefik/metrics versions and HA registration/externaltrafficendpoint
remain unconfirmed. API HPA adds pods, not VPS; DB/Redis are still separate failure
domains. Noproduction500 or million-user claim follows from manifests.

Next: configure protected validationenvironment; schema-check + real isolated API
admission; obtain separate provisioningapproval; mockalltransports/roleownership,
networkallow/deny, health/drain/upload/tmp, node/etcdquorumfailure, secretrotation,
offnodesnapshot+protectedtokenrestore, externalDB/Redis outage and actualcutover/
rollback rehearsals. Mainwebsite remains untouched. PHPwarningtext/source/runtime
was not supplied: no fabricated diagnosis or fix. See README for operational gates.

Official references verified:
https://docs.k3s.io/datastore/ha-embedded
https://docs.k3s.io/installation/requirements
https://kubernetes.io/docs/tasks/run-application/configure-pdb/
