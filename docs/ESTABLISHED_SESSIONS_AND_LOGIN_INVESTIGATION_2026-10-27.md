# ATL Xpress: revised established-session qualification, 27 October 2026

**The paced authentication ramp succeeded for all100identities. Phase A is not qualified; load testing has stopped and Phase B was not executed.**

The measured stage was invalidated by an incorrect message-order assertion I introduced in the harness. The existing API returns each selected page in chronological display order; my assertion expected newest-first. It generated three false correctness failures and caused an early abort. The brief sample also crossed the unchanged inbox p95 threshold. Neither that brief sample nor an invalidated run establishes a sustainable user limit.

No production traffic, merge, deployment, service restart, production migration, infrastructure purchase, Redis/proxy/VPN/billing change or EAS action occurred. Password hashing, admission limits and application code were unchanged. No500-session or further load run was started after this result.

## Reproducible source and environment

- Final attempted Phase A: [run40519482282](https://github.com/3my1234/across-backend-api/actions/runs/40519482282).
- Exact tested commit: `99f0f9e35760df9b0c5742552e5b511901956f1d`, branch `perf/business-chat-history`.
- API SHA-256: `16e17986f680b7c709e16f1a2065984057d4d3bd5e73c15cf6c9de0ca3158de7`, identical to the earlier successful mixed-workload candidate. `internal/` source diff against `a5e94496f90435a2610c3c46592af3d0ea33e055` is empty.
- Lab executable SHA-256: `9065953a200b6004774f2233a66a6b88f6ed06ff24eadba6e00bdf69865708be`.
- Existing public standard GitHub runner; fixed internal Docker lab without public ports/runtime external egress. No customer data, payment delivery, media transfers or full worker process.
- Large-busy synthetic fixture:100buyer/provider pairs,1,000products,1,000services,100,000marketplace messages,205,000shared messages,20,010rooms.100distinct actors selected for VUs.
- PostgreSQL16 capped at2CPUs/1GiB; each of two APIs at0.75CPU/512MiB/pool8; k6 at1CPU/512MiB; Redis/gateway each0.25CPU/128MiB. Full Compose configuration, dependency image digests, fixture hash and runner CPU/memory details are retained.

## Authentication separated from activity

The revised setup performs real password authentication sequentially, with at most one login in flight and a250ms pause after each login. It then checks all100returned tokens against `/auth/session` after the last login, before any measured VU journey starts. The identity list is distinct and each VU binds to its own authenticated token. Tokens are private setup data and are excluded from artifacts.

Warmup HTTP requests use `phase:warmup`; workload requests use `phase:measured`. Warmup latency has its own metric and is excluded from the successful-operation latency metric. Raw built-in k6 HTTP metrics include setup, so those totals/rates must not be substituted for the measured workload.

| Warmup measurement | Result |
|---|---:|
| Password logins attempted/succeeded |100/100|
| Final authenticated identity checks |100/100|
| Login503/429 responses |0|
| Login p50 |75.25ms|
| Login p95 |88.56ms|
| Login p99 |107.16ms|
| Final session-check p95 |0.687ms|
| Total warmup HTTP requests |204|

The additional four requests are readiness, an existing-token identity check, expected expired-token401 and expected outsider404. Those authorization denials passed. Password-admission observer snapshots report the unchanged limit2on each API and zero rejected password operations throughout this paced run. This proves that the chosen paced warmup worked in this lab; it does not prove sudden-login-burst capacity or native app retry behavior.

## Measurement and failure

The plan remained100constant VUs for180seconds plus15seconds graceful stop,3–12second think times and independent15-second conversation polling. Gates remained: successful per-operation p95<500ms/p99<1,500ms; unexpected failures<0.5%; zero identity/content assertion failures; zero normal429/503; both replicas; complete acknowledged-write reconciliation; polling-observation p95<17s/p99<20s. No threshold was relaxed.

All100identities bound to their authenticated VUs and incremented the session-start counter. The stage aborted after approximately1.1seconds of scenario activity; the whole k6 execution including warmup lasted34.03seconds. Only27initial chat journeys began, and none completed. No sampled VU time series or stable100-session interval was captured before abort. The configured/started population is not a completed sustained-concurrency qualification.

The incorrect assertion accounts for **all three** `content_valid` failures, tagged `history_order`. In `internal/controllers/provider_conversation_controller.go`, `conversationMessages` deliberately reverses the selected descending SQL page before returning JSON. Ascending display order is therefore the existing contract; the application did not suddenly reorder messages incorrectly.

| Successful-operation sample | Count | p50 | p95 | p99 |
|---|---:|---:|---:|---:|
| Marketplace history |52|81.10ms|184.54ms|211.85ms|
| Marketplace send |17|230.56ms|358.86ms|363.23ms|
| Idempotent message replay |10|124.71ms|330.33ms|363.98ms|
| Read receipt |2|62.01ms|81.20ms|82.91ms|
| Inbox |3|489.21ms|**542.27ms**|546.99ms|

These are raw startup samples, not accepted180-second percentile distributions. Inbox exceeded the500ms gate with only three observations; retain this signal for investigation rather than declaring a platform capacity limit or changing the gate. The early content abort prevented collecting a representative inbox distribution. No marketplace browsing, service browsing, Town Hall, Support or admin journey was measured beyond startup chat.

Measured client traffic:112offered calls,84recorded responses—52history200,17initial-send201,10replay201,2receipt204 and3inbox200. No completed measured request returned429/503 or another unexpected HTTP status. Twenty-eight offered calls lacked a completed response record when the generator aborted. The counter reports zero completed journeys and100interrupted VUs. No arrival-rate schedule was used; zero recorded dropped iterations does not mean an independent offered-arrival campaign passed. The approximately92HTTP responses/sec derived from a0.912second response window is **not a sustainable throughput result**.

CPU, Redis, pool and generator samplers ran, but the meaningful measurement interval was shorter than their cadence. The artifact therefore does not provide sustained PostgreSQL/API CPU, Redis usage, generator headroom or connection-pool-wait rates for100active sessions. Samples spanning warmup/recovery must not be relabeled as workload metrics.

## Abort reconciliation and recovery

The client retained17unique acknowledged-send journal entries, corresponding to17initial-send201responses. Ten completed replays passed the same-message-ID checks. The database contained25generated marketplace rows, no generated Town Hall/offer rows, zero duplicate bodies and two receipt rows at reconciliation.

The verifier reported `write_reconciliation_passed:false`: its current rule requires the total stored count to equal the acknowledged count and its key verifier also rejects an unequal total before emitting key-level results. Eight additional stored rows are consistent with requests committing while the client is interrupted, but the retained artifact does not independently establish the final outcome of each in-flight request. **Do not call this a17-write integrity pass, claim lost/duplicated customer messages, or discard the mismatch.** The independent every-acknowledged-key guarantee was not qualified by this artifact.

For a future approved campaign, the verifier should always check each acknowledged key exactly once and separately report additional commits with uncertain client outcomes. Preserve offered write keys before sending, keep confirmed and unconfirmed outcomes separate, require no unknown/duplicate/missing keys, and continue to fail overall qualification when the workload aborts. This is a test-accounting correction, not permission to weaken integrity requirements or alter application writes.

All12readiness probes over the following minute returned200/`ok:true` with internal request times2.478–5.784ms. Pool/admission observations remained available through recovery; no canceled acquires or password rejections were observed. Aggregate pool wait counters changed during the combined warmup/startup/recovery interval, so they cannot be assigned precisely to the sub-second workload. Artifact upload and disposable-lab cleanup both succeeded.

## Phase B status and launch-readiness finding

**Phase B was not executed because Phase A did not qualify.** The20/50/100login-burst investigator is a local, unexecuted preparation commit `a9e3d0b`; it is not a measured result. It records initial attempts separately from a hypothetical bounded Retry-After-plus-jitter model, captures admission snapshots and cgroup CPU, and leaves all security settings intact.

Source inspection confirms a password-processing bulkhead of two operations per API, non-queuing rejection with JSON503 and `Retry-After:2`. The earlier simultaneous-login attempt demonstrated such rejections. Their immediate code path is admission control; whether accepted password hashing also saturates or throttles the current CPU allowance remains an unmeasured question for the controlled burst experiment. Do not assume spare CPU or arbitrarily increase the limit.

The current local mobile source at `3fd8df706d79e5b479266423cb6d155299cc2edb`, `App.tsx`'s `authenticate`, makes one password-login POST. On failure it formats/displays the server error and HTTP status. It does not automatically retry password-login503or enforce/display its Retry-After delay; the retry helper for chat history is a different path. This is a source-inspected launch-readiness gap, not a device-tested result. Mobile code was not changed here.

## Corrections and evidence retained

The initial revised preflight attempt40518248037/`1d2878d` stopped before authentication because my helper incorrectly demanded JSON from the expected expired-token401. Its artifact/log remain separate. Commit99f0f9e corrected only that denial-body requirement while retaining the exact expected status.

After the measured failure, I corrected the chronological page assertion locally in commit `bb91642`. Node regressions now accept chronological pages and reject reversed, duplicate and invalid entries. No further load test was run. The abort-accounting correction described above is still a proposal, and the inbox latency signal remains unresolved. The new local preparation/correction commits have not been pushed or deployed.

Local evidence: `qualification/established-sessions-20261027/`, including both full artifacts/job logs, final measured run metadata, exact configuration/scripts/binary hashes, raw k6 summary/timeseries/acknowledgements, resources/Redis/pools, recovery, reconciliation and server logs. The offline analyzer filters warmup and measured traffic separately and returns qualification false. SHA-256 manifests and a portable evidence ZIP preserve the complete record. A redundant API request for the earlier setup's final metadata failed during collection; its complete job log/artifact are retained, and the final measured run's cleanup metadata was retrieved successfully.

## Next recommendation; no execution authorized by this result

Review the corrected ordering oracle, improve abort-safe key reconciliation, and inspect the inbox's three startup samples before a new isolated100-established-session attempt. Keep all latency/error/security/integrity gates unchanged. Only after a valid successful Phase A should the separately approved Phase B investigation run. A three-minute successful stage would still not replace the audit's30-minute sustained check, driver/GPS, concurrent media, full worker/vendor delivery, device rendering, production networking or500-session qualification.

**Load testing stopped at Phase A. No operating capacity claim or production release follows from this run.**
