# Independent generator qualification

This experiment executes the byte-identical expanded `deploy/session-lab/sessions.js` at500 established sessions against an isolated stateful synthetic HTTP target. It retains3-minute duration, initial3-second jitter, activity distribution, UUID generation, logs, JSON serialization/parsing, response-body retention, local message-history processing, polling, driver updates/offline/readback, and original thresholds. Authentication remains serial with250ms pauses, but the target does not hash passwords.

The target returns40 historical messages per conversation (348bytes of historical body each),25 catalogue/inbox rows with504bytes of synthetic description each, and small synthetic nearby rows. Writes/replays and peer-history observations are stateful; authentication and authorization are synthetic. These payloads are documented envelopes, not a verified production payload-size bound. No database or application correctness/capacity claim follows from this run.

The retained first run compared one-CPU and two-CPU generator arms with2GiB memory and46-byte tokens. The follow-up measures three generator CPUs with2GiB memory and conservative512-byte synthetic tokens; it is a separate payload envelope, not a matched CPU-only comparison. The mock target has a separate1CPU/512MiB quota and5ms response floor. The same standard public runner hosts both; its four CPUs are not independent hardware. PostgreSQL/API/workers are absent to isolate generator cost. Therefore a passing arm establishes independent headroom only; the next full test must also retain headroom under actual shared-host competition or use a separately qualified runner.

The gate requires all500 sessions,180seconds of offered/completed accounting, no dropped iterations, original content/latency thresholds, first-activity spread<=5seconds, >=170seconds telemetry, generator CPU p95<70% and initial6-second p95<85% of quota, <1% throttled periods, memory<70% of2GiB, noOOM and mock CPU p95<70%. Host vmstat and100ms per-container cgroups are retained. These conservative independent gates are additional to, and do not relax, application gates. Closed-loop user journeys do not promise a fixed request-arrival rate; observed arrival bins must be reported.

Synthetic fixtures/tokens remain in generator-exchange, excluded from artifacts. No production traffic, application load test, infrastructure purchases or application pool/admission changes occur.


## Thirty-minute independent generator gate

`phase=generator-sustained` runs only the locally prepared synthetic 500-user arm at two generator CPUs and 2 GiB. It uses the existing `sustained` policy (1,800 seconds), existing HTTP actions, jitter, think times and original thresholds. Only the profile/duration and its explicit synthetic approval metadata change; the reversible local-preparation transformation is unchanged. No PostgreSQL/API/worker load or production traffic occurs.

The duration-aware gate requires at least 1,795 seconds between first offered and final completed measured requests, at least 1,790 seconds of generator CPU samples and 1,790 snapshots with 500 active VUs. It preserves the initial six-second CPU gate, overall CPU, throttling, memory, OOM, complete offered/completed/mock accounting and all original thresholds. It requires exactly 500 preparations and cached binding uses. A three-minute artifact cannot satisfy this gate.

The prior matched baseline/candidate proof is run 40747865966 at `41ccc8344a6974d43126b96343746bc4f4ed76d1`. Its script hashes and executed sources remain retained. The new run retains the baseline hash and executed candidate/reversible transformation again. Re-running the known failed initialization baseline for another thirty minutes adds no useful headroom qualification, so it is not included in this sustained phase.

The workflow is prepared, not authorized to dispatch before the root coordinator confirms both notification-batch regressions and the matched original/candidate benchmark have passed. After that confirmation, the coordinator may dispatch:

```powershell
gh workflow run worker-role-qualification.yml --repo 3my1234/across-backend-api --ref experiment/generator-headroom -f phase=generator-sustained
```

A passing synthetic result still requires actual-stack/shared-host headroom and application acceptance before any full 500-session application qualification. No 1,000-session or production action follows automatically.


The sustained preparation also fixes a synthetic-target identity race: response handlers formerly derived a new message ID from the latest global HTTP request count. Concurrent peer writes could therefore receive the same synthetic ID. A dedicated atomic message sequence now allocates IDs, while idempotent replay still returns the original ID. A deterministic concurrent-write regression failed against the old mock and passes against the correction. This is a mock-only correctness change; application code, request actions and client payload parsing remain unchanged. It is recorded separately from the duration change and must not be presented as an application fix.
