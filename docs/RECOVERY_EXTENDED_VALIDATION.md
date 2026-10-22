# Extended isolated recovery validation

No production operation is authorized by these commands. PR66 remains unmerged.
This adds offline tooling, not an API endpoint or startup/background task.

The structural drill can now accept a reviewed static Linux executable:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /private/recovery-read-check ./cmd/recovery-read-check
python3 scripts/restore-drill.py --archive /private/approved.dmp --postgres-major 16 --read-check /private/recovery-read-check
# Only after specific archive-handling and execution approval:
python3 scripts/restore-drill.py --archive /private/approved.dmp --postgres-major 16 --read-check /private/recovery-read-check --execute
```

Preview hashes both files and performs no Docker operation. The executable must
match the host architecture (amd64 for the current Contabo main host; arm64 would
need separate qualification). Build, pull/transfer and execution permissions are
separate from the preview. The image must already be local; the tool never pulls.

Extended mode retains the new isolated database until structural, business and
sampled real-handler checks complete. It creates a SELECT-only `recovery_reader`
role only inside `recovery_drill`; the role has no superuser, create-role or
create-database rights. Handler checks require Unix-socket access to that fixed
database and reject non-loopback network interfaces. No environment/.env load,
production URL/identity credential or listener is used. Empty integration config
and network-none isolation prevent live integrations. Workers and migrations do
not run. The tested write-denial statement targets zero rows even if permissions
were accidentally broader; permissions/read-only rejection is mandatory.

Actual application read handlers cover plans, business overview, paid subscribers,
stored payments, provider profile/documents/renewal preferences, provider/unified
inbox, ordinary buyer/provider history, direct shared history and Town Hall
feed/history. Missing sampled records are `not_exercised`, not silently PASS.
Ordinary message reads explicitly set `mark_read=false`; their default behavior
can otherwise update read timestamps. Stranger history requests must be denied.
This tests handler authorization with synthetic context, not login/JWT middleware,
device UI, external payments, push sound, every row or every application endpoint.
Response bodies and selected identifiers remain only in process memory, not logs.

Bundled SQL requires the fixed isolated database and runs read-only with bounded
timeouts. It counts schema/business coverage, checks ownership, thread pairing,
payment identities/amounts, subscription periods, duplicate retry/webhook identities,
Town Hall relations/seats, reply ownership and constraints/index validity. Nonzero
findings set `findings_require_review`; they require explanation before acceptance
and are never repaired automatically. Grouped financial summaries are executed but
not emitted as raw database strings. The real snapshot may legitimately lack data
that synthetic qualification includes. Restoration alone cannot prove snapshot
completeness without corresponding archive-time evidence.

Cleanup verifies ownership/network/mount isolation, removes only the owned nonce
container, and prints the success report after removal. Failures also run cleanup;
Docker/host failure can prevent it, so cleanup needs operator verification then.
Private source archive is preserved, not deleted. Resource limits remain 1 CPU,
1 GiB RAM and 512 MiB temporary PGDATA; check shared-host headroom and archive
expansion first. This does not certify host safety, total recovery/PITR, media
objects, original grants or production RPO/RTO.

CI applies real migrations to a disposable synthetic database, seeds actual
provider/subscription/payment/ordinary/shared/Town Hall records, dumps/restores it
and asserts expected counts, zero aggregate findings, real read-handler success,
stranger denial and cleanup. No real production archive is used in CI. Current
head must pass that job before real-data use.
