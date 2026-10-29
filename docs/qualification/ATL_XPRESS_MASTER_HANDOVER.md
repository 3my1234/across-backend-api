# ATL Xpress master handover

Updated 29 October 2026, Africa/Lagos. Read this current checkpoint first. The previous complete handover is preserved in `ATL_XPRESS_MASTER_HANDOVER_HISTORY_2026-10-29.md`; its historical execution instructions are not current commands.

## Current checkpoint

| Workstream | Verified state | Next gate |
|---|---|---|
| Notification batching | Implemented on the isolated backend branch. Correctness, concurrent retries, batch boundaries, final-page rollback, privacy and actual PostgreSQL receipt-worker recovery passed in **40854663425**, with no skipped required tests. | Full application capacity is not established by the microbenchmark. |
| Matched batching benchmark | SQL calls **1,100 -> 100** per100 transactions; cumulative SQL execution time **29?39% lower** across two ABBA runs. Controller-only throughput **2.37?2.58x**. | Preserve query-only scope; do not claim equivalent platform throughput or database CPU reduction. |
| Independent generator | **PASSED at4GiB:** 40866958782, source5dc1fc983b5e157d82ea86a6c546c0b2cb4f592c, original recorder/500sessions/1,800seconds/2CPU. Earlier2GiB run failed memory; gzip alternative failed startupCPU and was rejected. | Require complete sustained proof, original70%memory/85%burstCPU/70%steadyCPU gates and actual budget match. No application calibration yet. |
| Actual-stack calibration | **FAILED40872220566**, source85c543c; dispatched only after retained generator proof passed. Explicit8GiB PostgreSQL memory ceiling; DB CPU2, generator CPU2/4GiB explicitly requested; application limits and workload unchanged. The generator allocation must match its passed proof. | Requires the completed successful long-generator proof and exact measured client. This is bounded180-second calibration, not sustained500 qualification. |
| Full500/1,000 | **Not qualified.** No new actual application500 or1,000 run started in this continuation. | Shared-host/environment and application gates must pass before full500. No1,000 without a passing500 and review. |
| K3s | Separate preparation branch, strict schema **13/13** resources and **10** policy/source tests passed. Placeholders intentionally block release. | Three future VPS, isolated live staging, transports/network/recovery validation and separate approval. No installation or apply. |
| Mobile | APK63 accepted by the user; PR60 merged independently; all PR and postmerge checks passed. | Do not entangle mobile with backend qualification. Broader physical-device coverage remains distinct from visual acceptance. |

Generator40855200111 has completed and FAILED only memory headroom. Read `GENERATOR_SUSTAINED_RESULTS_2026-10-29.md`. Memory diagnostic40864170840 passed; lossless recorder validation40865534032 failed startupCPU and was rejected. Original-recorder sustained run40866958782 is running with explicit4GiB allocation. No short stage substitutes for sustained proof.

## Workspace and exact source references

Workspace: `C:/Users/EMMA/Desktop/gouwu`. This root is **not a Git repository**. Work within the appropriate worktree and inspect `git status --short` before editing. Do not overwrite another worktree or infer that an old background process is still running.

| Worktree | Branch | Exact references |
|---|---|---|
| `.worktrees/launch-qualification` | `experiment/launch-qualification` | Batch code `d5a854c2d00914b5db944f3f8bae8659599df134`; corrected no-skip CI source29ee9fa; calibration harness844af84; handover4790940; exact-client gate `d2b6bad367640f7a4565dbf4c79c2ced0a392c30`. Later documentation HEAD may differ. NOT merged/deployed. |
| `.worktrees/generator-headroom` | `experiment/generator-headroom` | Current sustained measured source `5dc1fc983b5e157d82ea86a6c546c0b2cb4f592c` (run40866958782, explicit4GiB). Historical2GiB source `ac854e5b597a564c96ed2bbddda7d14315261d74` failed memory. Its synthetic target intentionally excludes application batching. |
| `.worktrees/k3s-readiness` | `experiment/k3s-preparation` | `084717875d5a79356b3dd6e6e5471411814ec94d`; implementation8da2613. Deployment preparation only. Its historical application baseeb9ffd1 is not a production release candidate. |
| `.worktrees/mobile-login-overload` | `experiment/media-google-promoted-banners` | APK63 application source9758da7; fixture-only40c6862; mobile main merge `ee7546c6997d70494dedd8edd21f22a46803c82f`. |
| `.worktrees/notification-workers` | `perf/receipt-throughput` | Historical receipt source `d72df8b1f205886f9b03db889c79d5359d9628ec`; includes experimental migration108. |
| `.worktrees/inbox-performance` | `perf/inbox-page-work` | Historical inbox source67245f4 and its retained differential proof. |
| `.worktrees/expanded-capacity-ramp` | `experiment/expanded-capacity-ramp` | Historical fixture/importer/500 evidence. Do not dispatch its old workflow blindly. |

Backend repository: `3my1234/across-backend-api`. Mobile: `3my1234/across-mobile-app`. Provider PWA/admin: `3my1234/across-admin-panel`.

The tracked master is `docs/qualification/ATL_XPRESS_MASTER_HANDOVER.md` on the backend qualification branch. Its workspace mirror is `qualification/ATL_XPRESS_MASTER_HANDOVER.md`. Read local evidence reports under workspace `qualification/`; tracked repository reports may be under `docs/`. Keep mirrors aligned when updating.

## Product and current production infrastructure

ATL Xpress is a Nigerian marketplace with products/services, customer/provider accounts, subscriptions and Flutterwave payments, Town Hall requests/bids, direct/community chat, support/admin roles, durable notifications, and driver/courier discovery using leased location presence.

Backend: Go/Fiber, PostgreSQL, Redis, separate API and worker processes. Mobile: React Native/Expo SDK54, TypeScript and Expo Image. Provider web/admin are separate Coolify resources from the same web repository.

Existing independent Contabo EU hosts:

- **Main host173.212.219.169:** Coolify, original API, two API replica slots, gateway, worker, Redis, admin/provider web and an unrelated website. The unrelated website must remain online under Coolify.
- **Database host79.143.176.173:** dedicated external PostgreSQL, database `across_db`, remote Coolify server and private database connectivity. No destructive test or reset on this host.

Reported database sizing is approximately4vCPU/8GiB. Fresh CPU model, allocated memory, storage/IOPS/latency, settings, network RTT and failure-domain inventory are not established by that description. Do not label the shared lab production-equivalent.

No production numeric safe-traffic limit has been established. Historical healthy containers, a passing100-session isolated test and a local backup readability check do not prove broad launch readiness.

## Notification batching and correctness evidence

Files: `internal/controllers/community_controller.go`, `community_fanout_test.go`, `community_fanout_capacity_test.go`.

The helper selects at most64 eligible recipients in UUID order and uses an INSERT SELECT inside the caller's existing business transaction. It preserves active membership, sender exclusion, both block directions, generic private preview, recipient metadata, event keys, conflict deduplication and per-row trigger effects. Cursor progress follows selected recipients rather than inserted rows, so duplicate-only pages do not hide later recipients.

Tests cover privacy, rollback,140 recipients across three batches, four concurrent retries, new later recipients following duplicate pages, and a final-page injected failure that rolls back notifications, the business message and row-trigger enqueue effects. Existing Town Hall reply/authorization/seat-limit/expiry tests passed. Separate real PostgreSQL receipt tests executed claim/crash recovery/fencing, eligibility/drain, backoff, cancellation/device rejection and Retry-After checks.

Run40853697871 passed controller tests and ABBA but **skipped** receipt tests because an opt-in flag was missing. Preserve that result honestly. The corrected workflow/run40854663425 supplied `RECEIPT_TEST_LOCAL=true` and explicitly rejects skipped required tests; every required test executed and passed.

ABBA benchmark: same10-recipient synthetic fixture,100 transactions/arm, PostgreSQL16 at2CPU/1GiB. Each transaction verifies10 notifications and10 enqueue probes, then rolls back. SQL elapsed time is not exclusive CPU. The enqueue probe is not the full production transport chain; the later application test must still establish integrated behavior. Batch size bounds each statement, not arbitrary total room transaction duration; existing admission limits remain required. No pool/admission/security limit was increased.

Evidence: `NOTIFICATION_BATCHING_RESULTS_2026-10-29.md`; raw files and SHA manifests in `notification-batching-20261029/40853697871/` and `40854663425/`.

## Performance history and limits

Backend Actions links use `https://github.com/3my1234/across-backend-api/actions/runs/<id>`.

| Evidence | What it establishes |
|---|---|
| Original chat-history comparison | Approximately55->460requests/s, p95~740->80ms, query-only. Bounded/indexed pagination already exists; do not rewrite it without new evidence. |
| Matched mixed workload | Approximately38% throughput gain reported with reduced PG CPU; not independent production-session capacity. |
| Initial100 established sessions/30min, workers off | 80,898 requests,12,125 acknowledged writes stored once; notification backlog was expected with workers disabled. |
| Receipt sustained100:40627566223 | 81,186 requests,11,995 acknowledged writes stored once; actual workers with mock transports and real15-minute receipt eligibility. Receipt/send recovery and drain proof. |
| Expanded sustained100:40636465564 | 90,152 requests,14,372 acknowledged writes stored once; real Town Hall bids,487GPS updates,120offline transitions and3,036nearby searches; mock notification queues/receipts drained. |
| Importer timeout40688491116 | Preparation failed before measurement. Not a500-capacity failure. Named historical-import trigger handling corrected and independently tested. |
| Repeated500 bursts | Authentication completed, then overload/pool waits and ~2s aborts. Earlier569/1,243HTTP503 and p95~610/959ms. No sustained500 qualification. |
| Nearby40741981937 / handler40746907768 | Ordered cheap candidates followed by lazy eligibility, no candidate prelimit. Full result equality including revocations; query-only~1.33?1.86x. Handler/privacy/expiry/document tests passed. No Redis GEO/pool changes. |
| Independent generator40747865966 | 500synthetic sessions/180s,46,683offers=completions,~254.89RPS; no drops/throttle/OOM. Local preparation reduced initial CPU saturation while preserving the measured traffic. Not30-minute evidence. |
| Actual calibration40750457084 | Aborted1.863s:1,253offers,1,127responses,753HTTP503, successful p95~716.79ms. Generator p95~44.9%of2CPU with no observed throttling; PG2CPU saturated and hit its1GiB ceiling with385memory-max events. All43acknowledged writes stored once;52unconfirmed commits, no duplicate keys. |

The calibration's600notification INSERT calls/2,625ms span authentication, burst and recovery. That cumulative elapsed SQL total justified batching; it was not exclusive CPU attribution or proof that batching alone solves capacity. Preserve all failed runs and unconfirmed-commit accounting.

Relevant reports: `ACTUAL_STACK_CALIBRATION_RESULTS_2026-10-28.md`, `NEARBY_ORDERED_ELIGIBILITY_RESULTS_2026-10-28.md`, `GENERATOR_HEADROOM_RESULTS_2026-10-28.md`, `RECEIPT_CAPACITY_RESULTS_2026-10-28.md`, `EXPANDED_SUSTAINED_RESULTS_2026-10-28.md` and the archived complete handover.

## Generator gate and next justified action

Run40855200111 sourceac854e5 began its measurement step at10:09:32UTC/11:09:32Lagos, including serial authentication/preparation before1,800 measured seconds. Its final outcome is FAILED memory headroom. The subsequent focused diagnostic was analyzed, compression was rejected, and the original-recorder4GiB sustained run40866958782 is now the active gate.

Its gates require at least1,795 measured seconds,1,790seconds of CPU coverage and1,790snapshots at500VUs; original client thresholds, p95CPU<70%ofquota, initial6s p95<85%,<1%throttled periods, memory<70%of2GiB, noOOM/drops,500valid identities and complete offered/completed/mock accounting. The synthetic-target atomic message-ID correction is mock-only. This is generator qualification, not application/database/worker capacity.

Exact normalized client SHA256: baseline `db3013f473b41fc7d35e01d7385d620b247b977e3dd86e9e8683d14a4bb337a3`; prepared `dad72e9b2b8dd7dd9fca94bb6a0d6856d4081a49d301b53e3e133c127d01ea49`. Calibration now checks both baseline and downloaded executed-client hashes. No weaker thresholds or substituted tokens.

Only after a complete successful artifact review:

```powershell
gh workflow run worker-role-qualification.yml --repo 3my1234/across-backend-api --ref experiment/launch-qualification -f phase=calibration -f generator_run=40866958782 -f calibration_memory=8g -f calibration_generator_memory=4g
```

This performs a bounded180-second actual-stack calibration, with unchanged API/pool/admission/workload limits. PostgreSQL RAM8GiB/CPU2; generator2CPU/4GiB explicitly declared and matched to proof; host memory gate requires14GiB available. A1GiB control remains explicit if later needed for RAM attribution. Do not repeatedly rerun the undersized allocation without a question to answer. A4-core shared runner cannot independently supply4DB cores +2generator cores +APIs/workers; observe host contention rather than declaring production equivalence.

Collect complete resource limits, timestamped CPU/cgroup memory/events/I/O, per-operation latency/errors, database locks/pool waits, write reconciliation, queues and recovery. Original application gates and actual shared-host/generator headroom must pass before the unchanged30-minute500 qualification. No automatic escalation to1,000. If a prerequisite fails, preserve evidence and identify the smallest supported correction; do not hide errors or purchase resources.

## K3s package: preserve separately

Target: FIVE hosts. Existing main VPS retains Coolify and the unrelated website; existing database VPS remains external PostgreSQL; THREE future independent VPS form the K3s server/application cluster using embedded etcd. The old four-host/reuse-main option is superseded. No installation on either current host.

Branch `experiment/k3s-preparation`,084717875d5a79356b3dd6e6e5471411814ec94d. Files include `deploy/k3s/staging.yaml`, `README.md`, `server-config.example.yaml`, `validate.cjs`, `validate.test.cjs`, a separate migration-job example, and manual validation workflow `.github/workflows/k3s-preparation.yml`. Existing migration example is not a job execution.

Strict kubeconformv0.7.0 with Kubernetes1.35.0 schemas:13/13resources valid, no invalid/error/skipped entries. Ten offline policy/mutation/source tests passed. This schema baseline is not a chosen production K3s version or live admission/failover proof.

Package includes bounded API/HPA/pool configuration, nonroot/read-only/tokenless pods, dependency-aware readiness versus liveness, startup/drain allowances, hard host separation and a zero-surge/one-unavailable rollout that fits three nodes. Worker replicas remain0 and egress denied until real transports/roles are qualified; push send/receipt URLs are hardcoded Expo endpoints and browser-push destinations are stored URLs. A lab mock adapter is not automatically a real-worker staging transport. Isolate DB, Redis, object storage and every external delivery path before enabling workers.

Remaining: actual new-host inventory, private IPs/nonoverlappingCIDRs/MTU/CNI enforcement, pinned K3s/ingress versions, HA registration/traffic endpoint, secrets and exact trusted proxies, old/new DB connection overlap budget, all worker roles/ownership, etcd snapshots plus protected server-token restore, external DB/Redis recovery, alerts, node failure, rolling rollback and live staging acceptance. GitHub Environment reviewers must actually be configured before claiming enforced approval. Placeholders intentionally block release; no live apply/deploy path is authorized.

See `K3S_PREPARATION_HANDOVER_2026-10-29.md`. Additional nodes alone do not make PostgreSQL, Redis or external ingress highly available.

## Mobile status, kept separate

User tested APK63 at9758da7 and approved applying it. Mobile PR60 mergedee7546c; final fixture-only40c6862 does not change the tested app. Version63 EAS build5a4ba444-89cf-461a-80b8-7d67b295e378 finished. Ads use listing-shaped frames, captions, dots and safe rotation; visible service photos are prioritized and render batches bounded. Gallery fallback, Google handoff and bounded login overload handling are included. Registration is not automatically retried and password/admission protections remain unchanged.

All49native-provider suites and targeted gallery/auth/ads/cache/recovery checks passed. PR launch40842461051, discovery40842461066, Android40842461077 and postmerge40842584350 all succeeded. GitHub merged immediately with --auto before every check finished; remaining checks subsequently passed. Do not assume --auto enforces required checks in future.

Evidence: `ANDROID_WIDE_ADS_BUILD_2026-10-29.md` and `ANDROID_ADS_GALLERY_ACCEPTANCE_2026-10-28.md`. No new APK/store release is required for the fixture-only merge. Visual acceptance does not prove every Android/gallery/auth/network combination or a measured image-speed improvement. Server-side thumbnails remain a separate possible follow-up.

## Production safety and unresolved launch gates

Last operator inventory is historical October28, not a new live inspection:

- Main host: backend30b0b68/API+two replicas+worker healthy, no restarts/OOM; backend production/security flags passed; replica timeractive/serviceinactive. Admin/provider web068ff62 in the last reported inventory.
- Public-readiness403 with default Python UserAgent resolved to200/all8checks using a descriptive read-only UserAgent. No routing/security bypass was changed.
- DB schema107; one snapshot showed zero pending email/native/browser queues. This is not drain-rate proof. Local archive558,545bytes, modified18:17:17.534887UTC, full inventory/decode passed; Coolify backup completed18:17:31UTC with `atlxpres-db-backup-storage` listed. SHA256 `a7c59da4945377249200266d4227e3a56a1c22eed4497dce597eaf2ee4e96c87`.
- Archive readability is not off-server retrieval, restore/PITR or measuredRPO/RTO. Fresh release-matched rollback, independent restore, external alert receipt/on-call, firewall/security verification and production traffic limits remain incomplete.
- Receipt migration108 exists only in experimental backend work. Batching adds no new migration. Existing production schema is107 in retained evidence. Old/new worker receipt ownership needs a reviewed release sequence; do not migrate or mix roles by assumption.
- Historical private rollback directories remain on the hosts, including backend `/root/atlxpres-release-rollback/20261026T092109Z-573ce9bb` and web `/root/atlxpres-checkout-media-web-rollback/20261026T100016Z-f4c1e455`. Do not paste their private contents or assume they are current.
- The mentioned PHP security warning is unresolved; its advisory/CVE/package/version and affected site are not established. Do not invent a diagnosis or mark it patched.

No purchases, K3s installation on existing hosts, backend main merge, production migration/deployment/restart, DNS/ingress/firewall/VPN/proxy change, Redis/PostgreSQL migration/restore/failover, secret/billing change or traffic cutover without explicit approval. Synthetic lab only; no real email/push/payment delivery. Preserve Coolify/unrelated website and external PostgreSQL. Mobile approval does not authorize backend release.

## Continuation checklist

1. Verify clean worktrees and actual generator run/artifact; finish the independent1,800-second result analysis.
2. If it passes, dispatch the gated8GiB bounded calibration using the exact command above; preserve failures as well as passes.
3. Validate actual shared-host/generator/DB and application gates before adapting the existing unchanged sustained500 workflow to the explicitly qualified source/environment. Retain original latency, integrity, authorization, bidding, retry and queue-recovery thresholds.
4. Keep1,000 gated. If calibration fails, identify its specific blocker and propose only an evidence-supported correction or honest test-environment requirement.
5. Keep K3s preparation intact. Await three new VPS and separate provisioning/installation/cutover approval. Do not disturb current production roles.
6. Update this master, its tracked mirror, reports and exact commit/run references after each result. Use `BRANCH_STARTER_MESSAGE_2026-10-29.txt` only with this current checkpoint.

## Current blocker update: generator memory

Long run40855200111 completed1,804.592seconds with449,839offers=completions,500identities,1,800snapshots at500VUs, no dropped work/OOM and all original workload thresholds passed. Generator CPU p95 was17.43%of2CPU quota, burst31.45%, throttled periods0.0166%. Peak total cgroup memory reached~2GiB, failing the unchanged70%of2GiB headroom requirement. NOT qualified.

A1.66GB generated metrics file may contribute through filesystem cache, but memory.stat was not recorded; process versus file-cache attribution is UNKNOWN. Focused180-second same-client/same-cap diagnostic40864170840 at5dce8fe now records that breakdown. No actual-stack calibration, full500,1,000 or production work was started. Do not bypass the failed gate or point calibration at the short diagnostic; it requires a passed1,800-second artifact.

Backend qualification branch advanced only in tooling/docs: exact-client proofd2b6bad, consolidated master/archive/offline analyzer4672407, optionalI/O telemetry fix015fb92. Application source remains the previously tested batch candidate; no internal/migration/session-client changes. K3s branch0847178 and mobile merge remain separate.

## Current recorder validation checkpoint

Run40864170840 passed the focused183.164second stage with46,686requests. Peak total1.034GB comprised~849MBanonymous +175MBfilecache; the175MBmetrics file closely matches filecache. A lossless FIFO/gzip recorder was implemented atb6e475b on experiment/generator-headroom; it runs INSIDE the unchanged2CPU/2GiB generator cgroup, with complete raw metric reconstruction after measurement and unchanged client hashes, thresholds and memory accounting. It does not offload generator work into an unmeasured container. Recorder contract and focused stage validation40865534032 are RUNNING. If and only if these pass, repeat the full1,800second generator gate. Current long result40855200111 remains FAILED. Calibration and actual500 still blocked; no production changes.

## Latest checkpoint ? explicit4GiB generator qualification

Compression validation40865534032 FAILED only startupCPU:88.93%of2CPU >85%gate, despite reduced peak memory831MiB and correct46,686requests. Reject it; direct recorder restored. Existing long2GiB result40855200111 remains FAILED memory.

New sustained run40866958782, exactsource5dc1fc983b5e157d82ea86a6c546c0b2cb4f592c, is RUNNING on experiment/generator-headroom. Same500syntheticidentities, client hashes, actions,2CPU,70%memory/85%burstCPU/70%steadyCPU thresholds; EXPLICIT4GiB memory allocation addresses measured trace+process footprint. This changes allocation, not threshold fractions, and uses only the existing standard isolated runner. Gate rejects mismatch between requested budget and actual DockerMemory. No resources purchased. Do not call a futurepass a2GiB result.

Calibration branch4d658ef uses original recorder and requires requested generator allocation to match the successful sustained artifact. The prospective command is now `gh workflow run worker-role-qualification.yml --repo 3my1234/across-backend-api --ref experiment/launch-qualification -f phase=calibration -f generator_run=40866958782 -f calibration_memory=8g -f calibration_generator_memory=4g`, ONLY after complete successful artifact review. DB8GiB/gen4GiB requires14GiB availablehostmemory; failure stops before workload. Do not dispatch against earlier failed runs. No application500/1,000 or production changes.

## Sustained500 preparation (not dispatched)

Workflow/glue8a5796b, calibration analysis3e0b432 and provenance1f01ea1 prepare explicit phasequalified-500. It preserves original measurement, reconciliation,60srecovery, send drain and real receipt-tail bodies and requires successful generator/calibration artifacts plus a source-matched shared-host/database review record. The record file is intentionally absent, so premature dispatch fails before lab preparation. No target1,000 is exposed in this path. See QUALIFIED_500_EXECUTION_2026-10-29.md for exact prerequisites, limits, files and remaining validation scope. No app/runtime changes or activefull500run.

## Tooling provenance checkpoint

Qualification tooling HEAD6971894a148c45fb2f403defc637e680b294d0fb is committed and pushed. It retains exact /app/api and /app/lab binary hashes for any future gated sustained workload. Application/internal/migration/session-client source remains unchanged; no actual-stack calibration or sustained500 run has been dispatched. Verify observer coverage over authentication plus measurement and recovery from the retained timestamps; a configured observer lifetime is not proof of complete coverage.

## Latest checkpoint: sustained generator PASSED; actual-stack calibration started

Run40866958782 completed successfully at source5dc1fc983b5e157d82ea86a6c546c0b2cb4f592c. Retained gate, summary thresholds, actual Docker4GiB allocation and executed-client hash reviewed; all33 artifact files have streamed SHA256 checksums in qualification/generator-headroom-20261029/40866958782. 449,804offers=completions over1804.656seconds (~249.25RPS),500identities/1800fullVU snapshots; generator CPU p95 19.95%of2CPU, startup61.02%, throttled periods0.0222%, peakmemory2,687,139,840bytes (62.56%of4GiB), noOOM/drops. Original gates passed. Synthetic target only, NOT application/database/worker capacity.

Bounded actual-stack calibration40872220566 was dispatched on experiment/launch-qualification at85c543c with generatorproof40866958782, DB8GiB/2CPU, generator4GiB/2CPU, unchanged application/workload/pool/admission limits. It must verify14GiB hostmemory and all source/endpoint prerequisites before preparing the disposable lab. Calibration is RUNNING, not qualified. Full500 and1000 remain blocked pending retained actual-stack results/resource review; the sustained approval record remains absent. No production or infrastructure changes.

## Latest checkpoint: calibration40872220566 FAILED ? exact cause established

Read CALIBRATION_FAILURE_40872220566_2026-10-29.md. Full45fileartifact12896898726 downloaded/hashed. First Python traceback was transient cgroup-startup readiness inside retry loop; it later passed. Terminal gate correctly rejected measured_exit99/reconcile_exit1/recovery_exit0. All500sessions started;1.723second burst produced707HTTP503 from909responses/1058offers. DB2CPU saturated/throttled; DBmemory~1.917GB/8GiB withnoOOM/limit events. Generator had observed CPU/memory headroom; gateway0.25CPU also throttled. No sustained application capacity established.

Five acknowledged chat/bid writes stored once,0duplicates/missing acknowledged keys,38unconfirmed commits. Presence31OFFER/13ACK failed strict complete-inventory verifier afteroverload/abort; expanded final-state correctness didnotpass. Do not claim the traceback alone was a setup defect or that full correctness passed. Recovery readiness passed.

Only gate diagnostic messages improved and replayed against retained failure: still rejects99/1. No application or threshold changes, no calibration rerun. Next supported investigation is complete notification batch/row-trigger cost and quota/shared-host attribution before a targeted correction or rerun. Full500/1000 remain blocked, reviewrecord absent. Generator40866958782 remains passed. Production/K3s/mobile unchanged.

## Targeted continuation checkpoint

User read-only production host output confirms4visible AMD EPYC vCPUs/KVM,7.8GiB RAM,7.1GiB available,no swap. Does not prove effective PostgreSQL container quota or CPU guarantees. Gateway probe40876893888 PASS all3000requests; directp95~26?31ms,existingproxy0.25CPU~257?285ms,1CPU~69?70ms. SeeGATEWAY_QUOTA_RESULTS_2026-10-29.md. Application admission719counterrejections includinglatecompletions confirmsgatewayalone didnotaccountforactual503s.

Focused full-schema trigger/DBCPU ABBA40877066781 at886599f is RUNNING. APIs/workers stopped duringSQL; no original500workload repeated. Compare2/4/4/2CPU at8GiB usingactualsourcequery andmigrations, alltriggerenabled, EXPLAIN plus8client20secondpgbench; clientsseparatecgroup/monitoring. Cancelledpreparation40876618230 is superseded, noresultsused. Noapplicationchanges/productionoperations, full500/1000blocked.

## Latest checkpoint: full-trigger CPU profile PASSED; sender candidate under test

40877066781 at886599f PASSED actual migrated notificationtrigger profiling.2CPU359.6/422.2TPS vs4CPU645.1/640.2TPS,8clients/20seconds/arm,ABBA. All writes rolled back, nofailures, before/after delivery counts matched. These are SQL rollback-only measurements, not500session or commit/transport capacity. Trigger notification_sender3.65?3.87ms of9.73?10.43ms query cost gives a specific software target. Full50fileevidence/hash retained. Read NOTIFICATION_TRIGGER_CPU_RESULTS_2026-10-29.md.

Candidate sender-candidate.sql usesusers-only ordinarysender branch while preservingprovider membership/name/avatar fallback. It is NOT a migration or deployed change. Matched comparison40879057063 at526bed4 is RUNNING,2CPU/8GiB,original/candidate/candidate/original,4504identity outputequalities eacharm, realnative/webtriggers androllback invariants. Waitforactualresults beforeadoptingcandidate or rerunningapplicationcalibration. No500/1000started, noproductionoperations.

## Latest sender-comparison correction (read before older running checkpoints)

Run40879057063 at526bed4 is preparation-only, despite GitHubsuccess. The sender step was mistakenly attached to the querydiagnostic job; every measured step in actual-stack-calibration was skipped. It provides NO sender benchmark/equality result and must not qualify anything. Artifacts retained honestly. This doesnotinvalidate measured gateway40876893888 or actualtriggerCPU40877066781 results.

Workflow8367871 moves sender-profile into the correct job and requires4pgbenchoutputs,4plans,4equality files with>=4000cases/zero mismatches and original/candidate/candidate/original order. Local YAML/jobrouting assertions passed. Corrected run40881068109 is RUNNING at8367871. Waitfor actual retained results beforeadoptingcandidate or actual500calibration. SenderSQL remains isolated benchmarkcandidate, not a productionmigration.

Representative resource preparationcd0fd06 adds unused4DBCPU/8GiB+1gatewayCPU+2generatorCPU/4GiB overlay and failclosed12CPU/20GiBavailable hostpreflight. It is NOT wired into a workflow or provisioned; current4CPUrunner rejects. Read REPRESENTATIVE_CPU_CALIBRATION_2026-10-29.md. Noresourcepurchase/reassignmentauthorized. Userread-onlycheck of effectivePGDockerlimits ispending via collect_database_resource_limits_readonly.py; knownhost4vCPU/7.8GiB alreadyrecorded. Full500/1000/production unchanged.

## FINAL TARGETED CHECKPOINT: sender candidate REJECTED; resource gate remains

40881068109 at8367871 completed actualsenderABBA. Original235.26/245.27TPS vs candidate245.53/245.17TPS; trigger/querycostnotconsistentlybetter. All18016identityequalities and19417rollbacktransactions passed, but NOperformancewin. Rejectcandidate; no109migration orapplicationchange.60artifactfiles andSHAmanifestretained. Read SENDER_FAST_PATH_RESULTS_2026-10-29.md. Earliergreen40879057063 remainsprep-only, notvalidproof.

Targetedinvestigation nowcomplete. Nextjustifiedaction is representativeCPUcalibration on suitablyprovisioned approvedisolatedresources, NOT moreSQLrewrites orunchanged500reruns. Gateway andDBCPU effects separatelymeasured; current4CPUsharedrunner cannot independentlysupply4DB+2generator+APIs/workers/gateway budgets. Unusedrepresentativeoverlay/preflightcd0fd06 documents12CPU/20GiBavailablecombinedhostorreviewedsplitisolatedhosts. Nopurchases/provisioning/productionhostreuseauthorized. AwaiteffectiveDockerPGCPU/RAMlimits JSON requestedfromuser; host4visiblevCPU/7.8GiB alreadyrecorded. NoactiveCItestsleft, no newactual500calibration, full500/1000stillblocked. K3s0847178/mobileee7546c intact.

## Latest resource decision checkpoint

ReadTEMPORARY_CALIBRATION_LAB_APPROVAL_2026-10-29.md. Readonlyinventory:0registeredselfhostedGitHubrunners, ownerpersonalUseraccount(largerunnersrequireeligibleorganization), locallaptop4logicalCPU/7.9GiBRAM. Nosuitableconnectedfree/alreadyapprovedisolatedenvironmentfound; productionhostsremainexcluded.

Preparedunuseddedicated-compose.yml+offlineallocationvalidator:criticalrolesdisjointDB0-3/gen4-5/API6,7/gateway8/worker9;aux10/observer11;host12-15. Exactproposal:oneHetznerCCX43EU16dedicatedvCPU/64GB/360GBNVMe,up6hours,estimated~US$3.15beforetax/IPv4,requestedusagecapUS$10. Officialrates/specs/cpu/billingdocsverified;checkoutstock/quote/accounteligibilitynotestablished. NoVM/runner/firewall/paymentcreatedorchanged. NeedEXPLICITpaidresourceapprovalor suitableexistingisolatedhostbeforeexecution. OfflinemaptestsdoNOTproveactualresourceisolation. Newhostrequireslivecpuset/quotaancestry/headroomverificationbeforegenerator/calibration/full500gates. No1000,purchases,K3sorproductionchangesauthorized.

## LATEST USER OVERRIDE ? NO NEW SERVER PURCHASES

The user explicitly withdrew the paid-testing direction because of budget and the store deadline. The Hetzner proposal and approval request are WITHDRAWN; do not provision or treat an old pending approval UI as current authorization. Use the two existing ContaboVPS plus freeCI resources, starting with READ-ONLY assessment. No production-changing load test until resource findings, isolation/monitoring/cleanup plan are presented and approved. Keep production ATL Xpress, Coolify and unrelated website online; do not installK3s or change production infrastructure.

Read EXISTING_VPS_QUALIFICATION_PLAN_2026-10-29.md. New passive collector assess_existing_vps_readonly.py and operator wrapper Collect-ExistingVpsHeadroom.ps1 are prepared. AutomatedSSH BatchMode failedauthentication on bothhosts, no remote command executed. Operator asked to run ONE localPowerShell command; it checks bothhosts sequentially, writesreports locally and latestpointer existing-vps-assessment-latest.txt. Await reports, then read savedfiles directly; no need foruser to pasteJSON. CPUcalculation/redaction/PowerShellsyntaxchecks passed; no liveheadroomresult yet. Assess feasibility before assigning caps; co-hosting cannot guarantee zeroresourceimpact. Logicaltestdatabase/volume/routing isolation and liveabortwatchdogs are mandatory; productionPostgreSQLcontainer/across_db neverusedfortestwrites. FreeCIgenerator remainsqualified; multiple generatorsnotneededabsentnewheadroomevidence.

## LATEST CHECKPOINT: existing-host reports complete; bounded pilot preparation

Read EXISTING_VPS_ASSESSMENT_RESULTS_2026-10-29.md. Both latest saved reports are complete. Main CPU p95 26.32%, maximum 88.46%, available RAM 6.28 GiB; database CPU p95 5.63%, maximum 8.85%, available RAM 7.07 GiB. Both four visible vCPUs. Short observations support preparation only; main spike requires sustained CPU watchdog, no unrestricted load. Proposed aggregate main test ceiling 2 CPU/2.5 GiB and separate test PG maximum 3 CPU/3 GiB; qualified generator stays on free CI. No independently dedicated four-core claim. Exact executable manifests, temporary restricted access and fail-closed watchdog remain to validate before requesting setup approval. No setup, key changes, seeding, load, production changes or purchases executed. Full500 remains unqualified; no1000. Existing websites identified and external baseline healthy; retain all previous correctness/performance gates.

## Cohost implementation preparation checkpoint

Added deploy/cohost-lab offline split-host Compose renderer, owned-ID stop policy and dry-run Linux watchdog. Ten local safety tests and renderer isolation/digest/pool/admission tests passed. No Docker available locally, so live behavior is NOT claimed. Added manual free-CI safety workflow with disposable owned/unrelated container stop test, no production access. README lists remaining cgroup, restricted peer-lease/tunnel and cross-host qualification adapters; old single-host destructive calibration scripts must NEVER target production Docker contexts. No live setup/load approval requested yet because integration is not complete. No new SQL investigation, paid resources, K3s or production changes.

## Latest safety proof

Free CI40922287424 PASSED at c50fc3d5de7d27c759018ece64ba3f25b7c2996e. Ten Linux policy tests, renderer tests and actual owned-container stop/unrelated-sentinel refusal passed. Full log and metadata hashed locally. Read COHOST_SAFETY_RESULTS_2026-10-29.md. This is not full watchdog/SSH/cgroup integration or500 qualification. No live setup/load or production change. Continue concrete cross-host adapters and independent watchdog/lease validation before asking for host setup approval; do not repeat read-only collection or SQL investigations.
