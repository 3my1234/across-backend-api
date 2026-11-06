# Frozen-build staged VPS execution review

**Decision: APPROVAL REQUIRED. Preparation is complete; no live execution has started.**

Reviewed attempt: `cohost-ffd511286f3d418e`. Review files: `C:\Users\EMMA\Desktop\gouwu\qualification\singlehost-review\cohost-ffd511286f3d418e`.
Frozen backend: `baa03e4aea43db055c2a0e7622e20dc047a1f23a`.
Executable SHA256: `5c42473a27a48d3c003d4047d438c31005907fbca2cf85f7a7e1e51f54a7fb16`.
Frozen execution source: `b72174fc71bfcdae1e7ccceda856769a4ea76e6c`, published as `experiment/approved-singlehost-b72174f`.

## Completed readiness evidence

- Exact frozen executable passed the original authenticated100-session/180-second disposable workload: [42245254946](https://github.com/3my1234/across-backend-api/actions/runs/42245254946). p958.33ms,9361measuredrequests, zero unexpected/controlled overload errors,1513offered/acknowledged/storedwrites; cleanup passed. These timings are NOT VPS predictions.
- Exact execution source passed coordinator/restricted-SSH/HTTP-forward/watchdog fault-stop/native-import guard/systemd-deadline cleanup integration: [42262818567](https://github.com/3my1234/across-backend-api/actions/runs/42262818567). Actual running executable identity was checked over restricted SSH; downloaded executable hash matches the100-session binary above.
- PostgreSQL16 release regression/race checks passed: [42245174772](https://github.com/3my1234/across-backend-api/actions/runs/42245174772).
- Original free500-session generator proof42178904513, original generator source and acceptance gates retained. No historical-artifact download is required at live runtime.
- Local review-file hashes, API/snapshot hashes, full snapshot inventory/restore proof, source immutability and launcher syntax passed. New approval remainsfalse; no new SSH key/private credential directory, GitHub secret or live test resources created.

No further backend optimization or new instrumentation is included. The API queue remains bounded at two seconds/32waiters per room per replica, with retryable503 before transaction creation. The normal workload must still produce zero controlled overload responses.

## Sequence and advancement rules

| Stage | Sessions | Measured workload duration |
| --- | ---: | ---: |
| Baseline |20|45seconds|
| Previous-failure checkpoint |100|180seconds|
| Gradual ramp |up to500|180seconds|
| Sustained qualification |500|1800seconds|

Preserve the original sequence. No250stage or new five-minute stages. Scheduled traffic totals36minutes45seconds; baseline, upload, snapshot restore, session initialization, receipts and reconciliation add time. Allow roughly60-90minutes overall. The independent server job has a120-minute hard lifetime from installation; pre-upload baseline/upload occur before that lifetime starts.

A stage passes only when it completes its FULL measured duration/concurrency and every original gate passes: overall and per-operation p95<500ms/p99<1500ms; unexpected-error rate<0.005; controlled_overload.rate==0; valid content/session/authentication and generator resource/accounting checks; exact independent write inventory; current bid/presence state and original receipt/queue/correctness gates as applicable. Zero unexpected errors is the desired observed result; the original0.005 gate is not silently changed or described as zero. Report both rates and counts.

**100PASS:** continue the already reviewed gradual500/sustained500sequence. This does not authorize production deployment or public launch. **AnyFAIL/abort:** no advancement or immediate retry; stop generation, preserve partial evidence, reconcile and complete test-only cleanup. Diagnose the retained primary failure before proposing a correction. Aborted-run missing completion/coverage must be distinguished from generator resource exhaustion, and unconfirmed committed writes must remain visible.

**OverallPASS:**500sessions actually reach and sustain30minutes, with all correctness, generator, performance and production-safety gates passing and cleanup independently confirmed. Anything less remains partial evidence, not500qualification.

## Allocation and isolation

| Resource | Hard reviewed limit |
| --- | --- |
| Application group, including both API replicas |1CPU /2.5GiB|
| Separate synthetic PostgreSQL |2CPUs /3GiB|
| Aggregate test allocation on database VPS79.143.176.173 |3CPUs /5.5GiB|
| Free GitHub generator |2CPUs /4GiB|

Two API pools retain8connections each; test HTTP admission remains64per replica. CPU quotas are limits, not reserved physical cores. No allocation increase is authorized.

This is the previously approved isolated single-host placement on the existing DATABASE VPS; no new test containers on main VPS173.212.219.169. It tests the application at these caps, not the exact two-host production network topology. A private test PostgreSQL/Redis/database/bridge and synthetic accounts/storage are used. No production database credentials, customer data, production schemas or existing Coolify services are changed. No DNS/firewall/sshd authentication changes, purchases or K3s. No public test ports: generator uses the existing restricted HTTP-only SSH forward to private HAProxy; database forwarding/public exposure is disallowed.

Reuse checksum-verified existing image archive and full snapshot fromcohost-ad09262dac9a4cdd. Upload the new verified API executable and small reviewed tools only. A required missing cached image may be imported from the existing verified archive only under the existing independent guard; do not download/rebuild images on the VPS or bypass the guard.

## Just-in-time preflight and automatic stop

Latest a837 cleanup was independently confirmed in its saved root audit: zero exact test containers/networks/volumes/key lines, units inactive, protected containers healthy/API200. This is historical evidence, not assumed current health. Pre-upload baseline uses verified retained tools froma837, requires its finalized marker and checks that NO prior cohost containers remain. The existing operator additionally checks its historical46f3cleanup marker. Current available RAM/disk/CPU, production container health and public ATL readiness are checked before creation/load; a failed check stops this attempt.

Retain all existing stop rules: available RAM<2GiB; diskfree<20GiB; OOM; protected production unhealthy; CPU>85%for5seconds; I/Owait>10%for5seconds; two consecutive public health failures; three consecutive public latency breaches; stale guard lease>5seconds;120-minute hard stop. HTTP2second/observer2.5second policy and bounded DNS-only retries unchanged. Public probes are api.atlxpres.com readiness and atlxpres.com, not the unrelated website. Watchdog and hard-stop cleanup survive coordinator/local terminal disconnection.

Telemetry is unchanged: k6 per-operation latency/errors/throughput, pool counters/cumulative acquisition waiting, sampled lock waiters, SQL aggregate mean/max, container/cgroup CPU/memory/throttling, queue and production-health evidence. It does not provide true per-request pool/lock-wait percentiles. Do not mislabel sampled counts or averages as percentiles.

## Cleanup / rollback

Success, failure, timeout or interruption invokes the existing exact-owned-resource cleanup. Remove only this run's recorded/labelled test containers/networks/volumes/anchors and temporary SSH key; delete its temporary GitHub secret. Never Docker-prune or remove unrelated resources. Retain verified snapshot/images and evidence intentionally.

The canonical launcher performs a separate final ROOT read-only audit after the operator: absence of exact-run containers/networks/volumes/key lines, owned units stopped, cleanup markers, protected production health/API200 and no temporary GitHub trial secret. Enter the password locally when prompted. A restricted-key denial after key removal is not itself a production failure; the independent audit decides cleanup confirmation. On audit failure, preserve evidence and inspect this same run; do not rerun setup.

For a manual abort, use a second PowerShell window to cancel ONLY this nonce's GitHub job, if dispatched:

```powershell
$trial = (& gh run list --repo 3my1234/across-backend-api --workflow singlehost-rehearsal.yml --limit 30 --json databaseId,displayTitle,status | ConvertFrom-Json) | Where-Object { $_.displayTitle -eq 'Single-host trial cohost-ffd511286f3d418e' -and $_.status -ne 'completed' } | Select-Object -First 1
if ($trial) { & gh run cancel $trial.databaseId --repo 3my1234/across-backend-api }
```

Then request the existing restricted test-only cleanup (also works during setup if the temporary key is installed):

```powershell
& python 'C:\Users\EMMA\Desktop\gouwu\.worktrees\messaging-100-rehearsal\deploy\singlehost-lab\ssh_once.py' --private 'C:\Users\EMMA\Desktop\gouwu\qualification\singlehost-private\cohost-ffd511286f3d418e' --command cleanup
```

Keep the original operator window open for its final audit. If the key is already removed/unavailable, do not reinstall it or rerun setup; use the saved job evidence and independent read-only audit. The server-side watchdog/hard stop remains independent.

## Explicit approval and exact start command

This review is complete but unauthorized. Running the following command approves this exact isolated full sequence, conditional advancement, caps, stops and cleanup:

```powershell
& "C:\Users\EMMA\Desktop\gouwu\qualification\Start-Reviewed500.ps1" -ApproveTrial
```

Enter SSH passwords locally and leave that window open. Use the printed GitHub link for30-second phase heartbeats; gh watch's single job line is not a workload heartbeat. Do not run another copy. Without the flag, with a consumed/changed review, or after the fixture start window, the launcher refuses before SSH/setup.

The retained full fixture expires7November2026at06:30:40Lagos. This launcher conservatively requires starting **before7November2026at04:30:40Lagos**, leaving the120-minute window. It will refuse later; never falsify timestamps or reuse a consumed approval.

## Release boundary

A pass qualifies only this tested build/workload/capped isolated placement. It does not qualify500simultaneous signups, every driver/media/payment/device-delivery pattern or unrestricted production traffic. The mobile retry correction3448048onfix/community-overload-cooldown must be included in reviewed store builds; this server-test approval does not build, publish, submit, migrate or deploy anything to production.
