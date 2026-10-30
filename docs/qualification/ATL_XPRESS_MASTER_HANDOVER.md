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

## Latest continuation: disposable integration PASSED; setup-only approval gate

Primary integration run40930615454 at5c58e7b PASSED17policy checks, six simulated-sensor/real-stop watchdog drills, real exact-owned volume/network cleanup with unrelated data retained, and complete Docker postgres alias -> Unix socket -> restricted SSH -> PG traffic plus fixed read-only identity control. Earlier source69ff20d run40929823698 also passed Docker bridge/cleanup. SSH shell/non-test port forwarding denied. These are disposable CI proofs, NOT actual Contabo connectivity or500capacity.

Bundle run40930181592 at13d316c PASSED and produced new immutable image archive SHA256020bacefeffd12bdfa460c165b43b471f9f987b5da7579bdfd219137ceb2c366. Lab-only CPU annotation now accepts3; targeted TestDatabaseCeilingRemainsBounded/TestSessionFixtureBounds passed and4/200remain invalid. No API/query/migration/session-client changes. Prior40925392518 bundle was downloaded/hashed but is SUPERSEDED for fixture execution because its lab binary rejects3. Corrected bundle full download/checksum is running in managed exec session39227; check qualification/cohost-image-bundle-40930181592/LOCAL_VERIFIED.txt before duplicating any download. Metadata already downloaded/verified. No private keys or tokens uploaded.

Read COHOST_SETUP_EXECUTION_REVIEW_2026-10-29.md. New local review nonce cohost-6d6c5f9220874022, pointer cohost-review-latest.txt; expiring public-key scope/compose hashes saved, private keys separately outside Git/artifacts. Only false approval.template.json files exist; no authorization record or host installation. Stage-only code is default dry-run and requires current matching approval, fresh five-minute protected-site baseline and digest archive. Starts idle new PG/Redis only; parent cgroup caps, root-independent setup watchdog expire after15min. Full control denies seed without fixture_preparation_authorized and API/worker/gateway startup without load_authorized.

Main test pool8/admission64/rate600 unchanged. New lab-only trusted ingress172.29.248.1/32 preserves original distinct synthetic XFF users through loopback SSH transport; existing spoof/repeated-header Go tests passed. Must prove actual ingress behavior at runtime, not silently collapse500users to one IP or widen production trust. Latest offline renderer checks enforce these exact values.

Remaining specific dependency: actual endpoint/cgroup validation needs approved creation of test-only resources/temporary restricted SSH keys. Request ONLY setup/read-only verification, not500load. Full split-host fixture/reset/journal/unchanged-suite adapter smoke remains required before full test approval. No production/paid/K3s/mobile changes, no actual500started, no1000. Do not ask user to repeat passive or prerequisite collections; reports are saved. Existing production site health/cpu/memory baseline must be fresh at execution, not assumed from old snapshots.

## Final offline setup review checkpoint

Final CI40933059552 at0e2739a403350532c1a4bb9679894c37df60a949 PASSED17tests, all six real-stop drills, unrelated-data cleanup protection and full Docker/SSH/PG path including expiring forced keys and fixed identity command. Final logs/metadata/SHA256SUMS retained qualification/cohost-integration-20261029/40933059552/. Bundle40930181592 source13d316c full archive AND manifest now downloaded and independently hashed successfully; LOCAL_VERIFIED.txt exists. No active download remains. Old39227 session completed; do NOT redownload. Image source13d316c/API unchanged; harness source0e2739a.

COHOST_SETUP_EXECUTION_REVIEW_2026-10-29.md updated with exact nonce, caps, keys, commands, rollback, remaining adapter/runtime gates and proposed unchanged500sequence. Request SETUP ONLY: test container/slice creation, bounded image import and expiring restricted keys/read-only connectivity/cgroup checks. No seed/API/worker/500load approval requested or recorded. Actual VPS connectivity is not claimed. Live setup cannot be verified while endpoints/keys do not exist; this is the specific human approval dependency. No production changes, purchases, merges or K3s. Master/starter mirrored byte-for-byte. Local review request fields now mark archive_locally_verified true, but false approval templates remain.


## Authorized setup execution checkpoint ? 29 October 2026

User explicitly APPROVED setup-only on existing hosts, main aggregate2CPU/2.5GiB, separate testPG1CPU/3GiB, expiring restricted keys, read-only transport/cgroup checks. No load, fixtures, API/worker startup, production changes or costs authorized. Actual approval.json now recorded locally for reviewed noncecohost-6d6c5f9220874022, immutable compose hashes unchanged. Key expiry21:27:24UTC; operator wrapper refuses starting after20:27:24UTC.

Prepared qualification/Invoke-CohostSetup.ps1 (tracked mirror docs/qualification/). Needs operator-local SSH passwords because automated SSH cannot authenticate. Copies exact reviewed tools/archive through binary-safe SCP; pins DB host key through trusted authenticated operator SSH; fresh5min baseline each host before setup. Starts DBPG/mainRedis only, then sleeping probe/socat and capped main-toDB tunnel for fixed read-only identity; checks restricted CI-key status. No generator.

New operator_setup.py adds independent14min cleanup task, immediate cleanup on failure, serialized cleanup/creation and exact-owned existing cleanup/key-removal adapters. Successful connectivity setup also cleans disposable resources and temporary key lines, retaining redacted metadata locally; a future load test requires separate approval and fresh setup. Test image cache/evidence retained, no broad prune. Nineteen local policy/operator tests and renderer tests passed; PowerShell AST syntax passed. CI rerun for new orchestration pending. Actual host setup NOT yet executed. Do not claim actual VPS connectivity or500capacity.

Final operator CI40935400291 PASSED at9661864dd52c68c3b368287d4aca5f44a7382302:19policy/operator tests, renderer, six real-stop drills, exact-owned cleanup/unrelated-data refusal, restricted Docker/SSH/PG proof. Logs/metadata/SHA retained. Invoke-CohostSetup.ps1 AST/native-array-binding checks passed. Ready for operator execution with local passwords; no host mutation has yet occurred. Successful setup deliberately cleans owned idle resources/keys after collecting proof; actual500 remains separately gated.


## Setup attempt stopped safely at baseline ? 29 October 2026

Operator uploaded role packages and exact image archives to both hosts (main199MiB15m47s, DB199MiB25m52s). Main public baseline failed BEFORE stage calls: no test container/slice creation, image import, authorized-key installation, seeding or load. Fixed remote run directories/archive uploads remain and MUST be reused after hash verification; do not run original wrapper again (it refuses existing directories). Slow uploads were outside guardian window by design.

Independent local same-UA read-only probe at about20:10UTC: APIreadyHTTP200/oktrue; samnadoilandgas.sportbanter.onlineHTTP503; unlimitedenergysystems.comHTTP200; atlxpres.comHTTP200. This identifies an existing protected-site health blocker, not an application capacity failure or proof of container root cause. No production repair/restart authorized.

Added baseline-failure.json diagnostic output preserving exact failedURL/seconds/counts, unchanged2sec/health threshold. Prepared Collect-CohostBaselineFailure.ps1 + diagnose_cohost_baseline_readonly.py: one password-authenticated read-only mainhost check, fixed4HTTPprobes, matching affected-site container state only, testcontainercount and stagedfile-existence booleans. No env/log secrets, queries, uploads, changes or load. Await operator report qualification/cohost-baseline-failure-readonly.json. Existing keys expire21:27:24UTC; original wrapper startcutoff20:27:24UTC. Refresh restricted expiry safely if later continuation requires; never reuse expired approval silently.19localpolicy/operator tests still pass.


## Read-only baseline diagnosis confirmed ? 29 October 2026,20:12UTC

Saved qualification/cohost-baseline-failure-readonly.json received and inspected: APIready200/oktrue0.392s, Samnad5030.167s, UnlimitedEnergy2000.280s, landing2000.332s. No container matching Samnad domain in current Docker labels (includes stopped inventory). Test-owned container countZERO; main stagedimagesexist, manifest/locallease/cleanupcompleteabsent. Confirms baseline stopped before setup. DNS resolves Samnad to173.212.219.169; public response is Coolify503 Site not available fallback, not a latency-only failure. Earlier passive inventory had running/healthy route samnadoilandgas.sportbanter.online. No proof yet of why removed/unconfigured; do not restart/create production resources or remove safety check without clarifying intended site state. User asked whether site intentionally stopped/moved or should remain online.

Both uploaded199MiBarchives remain reusable; original wrapper MUST NOT be rerun because it refuses existing dirs. Future continuation needs small-tools-only/resume path with exact existingarchive hashes and refreshed key expiry if necessary, same approved caps/loadfalse. No paidresources/productionrepair/load authorized.


## User-confirmed retired site; resume prepared ?29October2026

User explicitly confirmed Samnad was removed. Its503 is expected retirement, not a production failure to repair. Removed ONLY https://samnadoilandgas.sportbanter.online/ from current watchdog URLS; keep APIreadiness,unlimitedenergysystems.com andatlxpres.com, existing protected-container inventory and all CPU/RAM/disk/health/latency thresholds unchanged. No production changes.

Resume-CohostSetup.ps1 now reuses SAME run and both uploaded imagearchives, verifies remoteSHA256 and refuses existingmanifest before uploading ONLY small updated role/tool packages. Fixed immutable compose hashes checked locally unchanged. Existing authenticated DBhostkey pin reused. Refreshes ONLY uninstalled restricted-key expiry to invocationUTC+3hours and records existing setup-only authorization; no load/fixtureauthorization. Fresh5min baselines required on bothhosts, same setup/cgroup/tunnel/cleanup sequence.19localpolicy/operator tests+PowerShell AST pass. Request operator use Resume-CohostSetup.ps1, NEVER original Invoke-CohostSetup.ps1 for existingdirs. Actual host connectivity/setup remains unproven until this succeeds.

Resume CI40947396466 atdfa1fcac4d24ddabad585ae4984f6d983eb78e50 PASSED19checks, watchdog real-stop/cleanup protection and restrictedDockerSSH/PG integration. Logs/metadata/SHA retained. Resume script ready for operator password entry; no renewed host mutation yet.


## Resume disconnected during DBbaseline; staged continuation prepared

User resume verified SHA256 of both remote imagearchives, uploaded small correctedpackages, and MAINfive-minute baseline PASSED. Database SSH then closed with255 during its baseline step; completion unknown. Stage calls occur strictly AFTER both baseline calls, so no testsetup/load was reached in this attempt. Do not claim CPU/capacity failure or proven SSHidle rootcause.

Resume-CohostSetup.ps1 now supports -ContinueStaged: skip all package/archiveuploads, validate enough temporary-key lifetime, preserve existingapproval/loadfalse, reuse baseline only through unchanged fresh5min/15minage/health validator, wait for any existing exactbaselineprocess before rerunning. Operator SSH calls add ServerAliveInterval15/ServerAliveCountMax6/ConnectTimeout20; no serverSSHsettings modified. PS AST and full script controlflow dry-run with Native calls mocked PASSED:no remotecommands/no uploads; embeddedPython compiles. Existing protectedsites/caps/thresholds unchanged. Only short restricted-status keychecks lackkeepalive because they are not long-running. Operator asked to run same script with -ContinueStaged. Do not rerun imageuploads or restart production.


## Windows SSH quoting correction ?29October2026

-ContinueStaged failed before remote Python execution: Windows PowerShell native argument passing stripped nested -c quotes, and bash rejected base64.b64decode syntax. This was an operator-wrapper defect; no baseline/setup/containers/load started in that attempt.

Removed inlinepython -c/base64 remotecommand entirely. RemoteBaseline now pipes exact ASCII Python source on stdin to SSH fixedcommand `python3 -`, preserving keepalives/hostkeyverification and exitcode handling. Verified actualWindowsPowerShell source parsing via stdin to Python, executed quote/semicolon/parenthesis smoke via same stdin path, and tested EXACTRemoteBaseline function with mockSSH: stdin matches source, finalremotecommandexactpython3 -, keepalivepresent,no network. PowerShell AST passed. Updated local+trackedwrapper. Continue same `Resume-CohostSetup.ps1 -ContinueStaged`; no uploads/repeatedprovisioning/threshold changes. SSH255cause remainsunconfirmed; do not call it proven idle disconnect.


## Database-host baseline exceeded wall-time gate before setup

Latest correctedstdin continuation MAIN5minute baseline PASSED. DATABASE baseline first APIreadyresponse healthytrue but measured4.255135seconds (>unchanged2sec limit), completed_samples0, so failed before stage. No setup/keys/imageimport/load. This is NOTHTTP503, nor proof APIhandler itself took4.26s: currentprobe total includes resolution, TLScontext/cert loading, connect/TLS, response and scheduler time. urllibsockettimeout2 is not an overallwalltime deadline; do not conflate them. No threshold relaxation.

Prepared Collect-DatabaseBaselineTiming.ps1 + diagnose_database_baseline_timing.py, one DBSSH/password read-only collection:3rounds of exactbaselinePython probes for3protectedURLs, curlDNS/connect/TLS/firstbyte/total comparisons andoneIPv4APIprobe, standalone TLScontextinitialization cost, proxyboolean(no secrets), existingbaseline/failure metadata andmanifestexists. NoSQL/setup/uploads. Only13HTTPreadprobes total, boundedtime; output qualification/database-baseline-timing.json. Syntax/PSAST passed. Need this actualDBhostnetworkevidence before choosing correction; don't start anotherblind5minbaseline or modifyprodDNS/proxy/firewall. Bothlargeuploadsretained.


## Timing evidence supports probe transport correction, not API rewrite

Read qualification/database-baseline-timing.json: exactPython probe API4.252/2.163/.111s, landing4.222/2.128/.088s; allHTTP200/APIoktrue. Curl samehostAPI.156s, landing.135s, Unlimited.055s; IPv4API.117s, TLScontextinit.043s, no proxyconfigured. Pattern consistent with serial address-connect fallback/cold connection-path delay; does NOT prove IPv6rootcause because slowPythonattempts were not phase-traced. manifest_existsfalse.

Changed ONLY safetyHTTPprobe to installedcurl with HappyEyeballs, TLSverificationON, HTTPSredirectsonly, max-time2(connecttime2), no retry/forcedIP/thresholdrelaxation; elapsed remains monotonic including processoverhead. API requiresHTTP200, strictoktrue, bounded8192byteJSON; websites body discarded. Missingcurl/TLSerror/timeouts/malformed/non200failclosed. Officialdocs https://curl.se/docs/manpage.html#--max-time and #--happy-eyeballs-timeout-ms support totaldeadline/addressracing.22localpolicy/proberegressions passed, including certfail/timeout/503/falseJSON; localrealcurl3siteprobe retained in terminal. ActualDBfresh5minbaseline still mandatory.

Updated small reviewedroletoolpackages. To install correctedprobe/refreshuninstalled3hkeys use Resume-CohostSetup.ps1 WITHOUT-ContinueStaged (stillNOIMAGEuploads), then unchangedbaseline/setupflow. Do not attemptContinueStaged until correctedprobeuploaded. Existingarchiveshashchecked/reused. LinuxCIpending; no actualsetup/load/productionsettings changes.

CI40952760898 at45322ff passed22policy/probe tests+realstop/cleanup drills, but SSHforwardreadiness failed after acceptedkey because oldCIkeepalive1s*2 expired. Retainedfailedlog. CI-onlyintegration commonkeepalive aligned5s*2 to existingruntime tunnel; no safety/resource/latency/correctness thresholds changed. Rerunrequired; do not reportfailedrun aspass.

Corrected CI40953015526 atae82f40a2b11d08da12657143b43f80e54b81843 PASSED22tests, realwatchdogstops, ownedcleanup/unrelatedprotection and fullrestrictedSSH/Docker/PGproof. Bothfailed40952760898 andpassing40953015526logs/hashesretained. Readyoperator Resume-CohostSetup.ps1 WITHOUT-ContinueStaged to send smallcorrectedprobe files, refreshuninstalledkeyexpiry and reusebothimagearchives. No paidresources/productionchange/load; actualhostbaseline/runtimeproofpending.


## Actual setup reached image import; cleanup complete; identity correction verified

User attachment46875789-6b17-4c7c-a665-4b060fbc4f29 inspected completely. Both publicbaselinesPASSED; DBstageguard/reaperstarted, exactarchiveimportreturnedzero, then DockerinspectofexpectedPGconfigdigest6a2e...notfound. Failure BEFOREanchor/composecreate; DBmanifesttestIDsEMPTY. Both evidencearchives copied locally qualification/cohost-runtime-cohost-6d6c5f9220874022/, bothcleanup.completemarkersconfirmed. DBsampleproductionhealthytrue,OOMfalse,CPUhigh0,IOhigh0,~7.49GBavailable. No500load/noapplicationstartup. Missing-unit cleanupwarnings expected forunitsnevercreated.

Local exactSHAverifiedarchive contains config6a2e... AND OCImanifest058acf... referencing it. Mobycontainerd image_inspect primarysource https://github.com/moby/moby/blob/master/daemon/containerd/image_inspect.go reportsIDfromtargetdigest; classicstoreusesconfigdigest. This is compatiblewith observedhost failure; actualhostselectedidentity will be logged bycorrectedresolver, not assumedfromuncollectedhostversion. No mutabletagfallback orimagechange.

Addedimage_identity.py: verifiesarchiveSHA, hashesboundedOCIconfig+manifestblobs, provesconfig-to-manifest/platformlinuxamd64relationship, acceptsONLYexactconfigORmanifestIDfromsamearchive; resolveDockerinspectIDandplatformmustmatch. stage_host writesseparatecompose.runtime.json changingONLYimageidentity, keepsreviewedcomposeunchanged, no pull/build; skipsreloadifexactarchivedimagealreadyavailable; retainsimage-import.log andimage-identities.json.25localtests passed includingwrongdigest/platform/missingidentity refusal. Actualverified5imagemappingretainedbundle/verified-image-identities.json. CI40955483705 at71a318a39e1db8c20735b0cfdf910e1530e2f6f0 PASSED25tests, REALDockerarchive/save/loadidentityproof, realstop/cleanup protection andcompleteSSH/Docker/PGproof. Evidence retained.

OLDnoncecohost-6d6c5f9220874022 is CLOSED, manifest/cleanupmustnotbeoverwritten/restaged. Successornoncecohost-2f12dc3ecbf8acb1 locallyprepared under EXISTINGsetup-onlyapproval, freshrestrictedkeys/privatefolder. Exactold/newComposeJSONequality verified after nonce/pathreplacement; SAME main2CPU/2.5GiB+DB1CPU/3GiB/pool8/admission64/rate600. No additionalapproval/cost/productionchanges neededforretrywithsameapprovedscope. cohost-review-latest.txt pointsnewrun.

Resume-CohostSetup.ps1 updated tonewrun. Remote preflight requiresoldcleanup.complete, rejectsoldarchivesymlink, createsnewroot0700dir andHARDLINKSoldalreadyuploadedimagearchive(noimageupload/noextraarchivecopy), verifiesSHA, uploadsONLYsmallnewpackages. Oldevidenceandoldownershipboundariesretained. Same pinnedDBhostkeyfromtrustedSSH reused. ToolpackageandwrapperPSAST+remoteBashpreflightsyntaxverified. Freshfive-minbaselines/cgroup/tunnel/runtimechecks stillrequired. Run WITHOUT-ContinueStaged fornewsuccesorpackage. load_authorizedfalse, nofixtureprep/APIworkerstartup/productionmodifications. Actualtransport500proofremainsPENDING.


## Fresh-directory preflight ordering defect corrected and EXECUTED in tests

UsernewattemptfirstSSHreturned1withoutdiagnostic. Foundconcretewrapperbug: aftercreatingnewrootandhardlinkingarchive, preflightrequiredrequest.jsonBEFOREsmallpackageSCPthatcreatesrequest.json. Freshdirectorythereforecouldneverpass. Ownimplementationerror, notcapacity/securitygatefailure. No stage/baseline/load invokedbythisattempt. Newrootmayalreadyexistwitharchivehardlinkbutnorequest; donotdelete/reuploadarchives.

Removedprematurerequestrequirement. PreflightnowrequiresarchiveandvalidSHA, refusesexistingmanifest, andprintspecificreasonformissingoldcleanup/symlinksource/missingarchive. Addedtest_ps_preflight.py executesACTUALwrappercommandinsixdisposablefilesystemcases: freshdirWITHOUTrequestpasses, partialdirWITHOUTrequestpasses, existingmanifestrefuses, missingoldcleanuprefuses, corruptedarchivefailsSHA, missingarchiveexplained. WindowsfixtureomitsPOSIXmkdir-mflagbecauseACLsnotPOSIX; LinuxCIexecutesEXACTcommandunchangedincludingmode700.

31localchecks+PSASTPASSED. CI40956528847 at2924fdd7b053a69565d224a9527756442fb59762 PASSED31testsincludingactualLinuxpreflightbranches, realDockerarchiveidentityroundtrip, sixrealwatchdogstops/cleanupownership andfullrestrictedSSH/Docker/PGpath. Logs/hashesretained. RuntimeactualhostsSTILLpending; no promises ofpass500. SamecommandResume-CohostSetup.ps1 WITHOUT-ContinueStaged; SAMEsuccessornoncecohost-2f12dc3ecbf8acb1, SAMEcaps, oldarchivesreused/noimageuploads. Exactfailurediagnosticsnowavailable; noapprovalrepeat.


## DB runtime caps actually verified; main null-IPAM handling corrected

Useroutputforcohost-2f12dc3ecbf8acb1: both baselinesPASSED, DBtestPGcontainercreated/started, restrictedkeysinstalled, verify_cgroup reporteffectiveparentverifiedroleDATABASE1container. Confirmsimageidentitycorrectionworks onDBhost andactualresourcecapverificationPASSED; notdedicatedCPU/500capacityclaim. Mainstage failed BEFOREmainmanifest/guard/containercreation: Dockerbuilt-innetworkIPAM.Config=null triggeredTypeError. Ownmissing-Docker-metadatahandlingdefect. Cleanupfinallyrunning; atinspectionmain evidence-tarSSHawaitingpassword, no localnewrunarchivesyet. Askeduserfinishremainingpasswords, waitPSreturn, tellDone. DO NOTstartanothercopy orclaimDBcleanupcompletebeforeevidence. IndependentreaperstillprotectsDBtest.

Addedvalidate_networks: treatsONLYnull/missingIPAM/configlistasempty; realoverlapsalwaysrejected, malformedtypes/CIDRfailclosed. Added--preflight: readonlyfixedapproval/port/subnet/Dockercgroupavailabilitychecks, noresourcecreation/noSQL. UpdatedwrappertocallmainANDDBpreflightBEFOREbaselines/DBcreation forfutureattempt. Added3regressiongroups+CIinspectionofactualbridge/host/nonenetworks.34localtestspassed. CI40959849606 at2e832401919c80b65be68d1e151ca7b778add5b4 PASSED34checks+REALDockerbuiltinnetworkvalidation+archive/save/loadidentityproof+allstop/cleanup/SSHproofs. Logshashesretained. WrapperPSASTpassed.

Current2fnonceisclosing/cleanup; doNOTrestageoroverwriteitsmanifest. AFTERusercompletescleanup, readsavedqualification/cohost-runtime-cohost-2f12dc3ecbf8acb1/{main,database}-evidence.tar, verifycleanup+productionhealth+exactresolvedimageIDs, thenpreparefreshsuccessorwithsamecaps/approval andhardlinkexistingarchive. No newcommandgivenwhilecleanupawaitspasswords. Existinglatestpointerstill2f; successorNOTyetprepared. No load/productionchanges/increasedresourcesauthorized.


## Cleanup confirmed; next same-scope successor prepared

UseransweredDONE. WaitedforDBSCPfilelock/transfer tofinish, thenreadBOTHsavedarchivesunderqualification/cohost-runtime-cohost-2f12dc3ecbf8acb1/. Bothcleanup.completemarkerspositive. DBmanifesthadexactownedPGcontainer+syntheticvolume/network; cleanupcompletedafterthatinventorycaptured. DBimage-identities.jsonprovesexpectedconfig6a2e...resolvedtoarchiveOCImanifest058acf... onACTUALDBhost. No needreinvestigateimagecompatibility. DBsample{}iscleanupabortoutput, notpositiveproductionhealthproof. Noactualmaintransportverifiedyet.

Preparedsuccessorcohost-ad5c7dfa8bb37a25 withfreshrestrictedkeys/currentexistingSETUPONLYapproval. Comparednew/oldComposeJSON:EXACTequalityafteronlynonce/rootpathchanges; SAME2CPU/2.5GiBmain,1CPU/3GiBDB, pool8/admission64/rate600 unchanged. ExistingtrustedDBhostkeypinretained. NewroletoolsincludevalidatednullIPAMfixandreadonly--preflight. Localreview-latestpointerupdated; privatekeysremainoutsideGit.

Resume-CohostSetup.ps1 updated tonewsuccessor. It stillhardlinksSHAverifiedarchivefromclosedoriginal6dserverdirectory(no199MiBtransfer), uploadsONLYsmallpackages, runsreadonlypreflightBOTHhostsBEFOREbaselines/DBcreation, thenapprovedsetup. PSAST+34regressionsPASSEDagainafterwrappernonceupdate. CI40959849606 PASSEDactualDockerbuiltinnetworkmetadata/cgroup-relatedpolicy/archive/SSHproofs. No newhostmutationyet. UsercanrundefaultResumecommand; avoidContinueStageduntilupdatedpackagesinstalled. No load authorized, no purchase/productionchange.


## Actual BOTH-host cgroup caps verified; tunnel SQL identity failed

Userlatestad5output: BOTHreadonlypreflights+five-minbaselinesPASSED. DBPGcreated/running,restrictedkeysinstalled,cgroupverified1container. MAIN13containerscreated,ONLYRedisinitiallystarted,restrictedkeysinstalled,cgroupverifiedall13. Mainverifythenstartedonlysocatbridge/sleepingdbprobe+boundedtesttunnel, cgroupverifiedagain. Unixsocketexisted,butfixedread-onlySELECTthroughitfailedrepeatedlywithinbounded5secbootstrap withserverclosedconnection. No API/worker/seed/loadstarted. This is a specificcrosshosttransportfailure, NOTproofPostgreSQLcrashed or500capacityfailure. NeedexactSSHjournalbeforecorrection; don'tblindlyextendtimeoutorrestartprod.

Maincleanup.completeconfirmedinsavedmain-evidence.tar; mainlastsampleproductionhealthytrue,OOMfalse,CPUhigh0,IOhigh0,publichealthfailures0,latencyfailures0. DBcleanupcommandoutputshowsexactPG/volume/networkremovedandkeysremoved; DBfinaltarcollectionstillawaitingoperatorpasswordat22:09UTC, so localDBarchivepending. Keepcurrentwindowopen/finishprompts; nonewsetupwhileitfinishes.

PreparedsingleCollect-CohostTransportFailure.ps1 afterPSreturns:readonlytworoleSSHcollector ofEXACTad5testunitjournals(dbTunnel/guard/reaper), cleanupmarkers,remainingnoncecontainers/keylineCOUNTS(no keycontents). NoSQL,noauthlogs/envs/noresources/productionchanges. Retainedjournalddataisneededbecausecurrentevidencearchiveomitsunitstderr. Outputqualification/cohost-transport-failure-{main,database}.json. PSAST/Pythoncompilepassed. DoNOTprepareanothernonceorissueanotherload/setupcommanduntilactualtransportcauseknown.

Pastedterminalcontainedcredential-lookingtext; doNOTcopy/repeat/store/useitinautomation. Adviseuserrotateifrealcredentialaftercleanup. No passwordstoredinartifacts. Existingad5runclosing;full500stillunqualified/loadnotauthorized.


## SSH succeeded; unpublished internal-only Docker endpoint identified and corrected

Both saved cohost-transport-failure-{main,database}.json read completely. BOTHcleanup_completeTRUE, remainingtestcontainersZERO, remainingtestkeylinesZERO. Mainexactownedtunneljournal: SSHstarted, repeatedchannelopen CONNECTFAILED Connectionrefused for127.0.0.1:55432; NOTkeyauthfailure/PermitOpenrejection. OriginaltestPGattachedONLYinternalnetwork while requestingDockerpublishedloopbackport. Dockerdoesnotpublishwithoutdefaultgatewaynetwork. Dockermaintainerexplanation https://github.com/moby/moby/discussions/53256 and official https://docs.docker.com/engine/network/port-publishing/ supportthis; noDNS/firewall/productionrepairneeded. PreviousCIprotocolproofincorrectlyusedPGonstandardnetwork; revisedtoactualinternal-onlyPGpath.

Fixhost_relay.py: fixedloopbacklistenerDB55432->exactownedlabelledinternalnetworktestPGIP:5432, resolvesIPONLYthroughimmutablemanifestcontainerID+networkID+nonce labels+subnet/platformchecks; noarbitrarytarget/nopublicbind. DBrelayunitunderSAMEdatabase.slice,25%individualCPU/64MiB/Tasks128; aggregateSTAYS1CPU/3GiB. PostgreSQLremainsinternal-only/NOoutboundnetwork. DBstageverifiesactualPGwireSSLreplyonloopbackbeforeSSHverification. RemovedineffectiveDockerportdeclarationsfromPGandfuturegateway. Preparedmainrelaytarget58080->ownedgateway8080REQUIRESload_authorizedtrue andISNOTSTARTEDbysetup. Noprodpools/routing/firewalls/DNS/SSHconfigchanges.

CleanupstopsONLYexactnoncerelayunittoo, automaticallyretainsdedicatedtestunitjournalsintransport-diagnostics.jsonBEFOREcleanup; errno-onlyrelayerrors(noSQLpayload/secrets)retained. Wrapperarchivesdiagnostics. All38localchecksPASSED, includingrealduplexTCP,wrongowner/network/address/publiclistenerrefusal,mainloadapprovaldenial. CI40964741748 at5b0ed28912d0b9750a18bbca90f80295e4302f6c PASSEDfullcorrectedpath:maininternalDockerpostgresalias->socat->UnixSSH->DBloopbackrelay->internal-onlyPGWITHOUTpublishedport; repeatedidentity,forcedcommand/forwarddenials,allrealstops/cleanup/archive/networkproofs. Exactsmallerrno-loggingrefinementdc48c6e triggersCI40965034937 currentlypending.

Newsuccessorcohost-0cfe085cbad1ec57prepared; oldad5closed/evidenceintact. ExistingexplicitSETUPONLYapprovalpersistedwithSAMEcaps,restrictedkeytargets,pools/security+noapplicationload. NewComposecomparedtoad5afteronlynonce/pathreplacementANDremovingineffectivePG/gatewayportdeclarations; nootherresource/envdifference. PinnedDBhostkeyretained,fresh3hexpiringkeysoutsideGit. WrapperdefaultResumeupdated tonewrun, stillhardlinksexistingSHAverifiedarchivefromclosed6d(noimageuploads). ReadonlypreflightBOTHhosts+freshbaseline+cgroup/tunnel/cleanupremainmandatory. No500/seed/API/workerstartup authorized. ActualfixedhostconnectivitySTILLpending.

FinalrelayCI40965034937 atdc48c6ec9c33b7f96ce60bdee3f59e7d5adb58b9 PASSED38tests+allcorrectedendtoendproofs; supersedespendingstatusabove. New0cfeexecutionpackagecontainsEXACTtestedrelay+operatorcode, logs/hashesretained. DefaultResumecommandreadyforoperatorpasswords; noactualnewhostmutationyet.


## Current0cfeuploadcompleted; DBSSH extraction connection closed; safe continuation added

UserdefaultResume0cfe: both existingimageSHAchecksOK, main156KiB/database146KiBpackageuploads100%. DBSSHclosed255duringextractionconnection, BEFOREreadonlypreflight/baselines/stage. No newtestcontainers/units/keysstartedbythatattempt. Cannotassumeextractionranor rootcausewasidle: verifyfileoutcome. Localprocessinspectionfoundonlystalead5evidence-tarSSHpending; closedEXACTownedoldlocalSSHprocessafterBOTHhostcleanupzero-resource/keyreportsalreadyconfirmed. Nostoppedremoteservice/legitimateinteractiveSSH.

Addedcomplete_staged_package.py transportviaSTDIN (no-cquoting). ContinueStaged nowchecksBOTHroles: ifsetup.tarremainsvalidateallmembers(noabsolute/traversal/link/device), safelyextract; ifalreadyextractedskip; verifySHA256ofreviewedcompose/request/approval+allPython toolsagainstlocaluploadedrolepackage, assertnonce/role/loadfalseapproval, rootprivatepermissions, removepackageonlyaftervalidation. RefuseANYexistingmanifest. Nevercreatecontainer/network/service/SQL/load; no uploads. Existingremoteimageshardlinksuntouchedexceptroot-onlypermissions.

3newfixturetestsexecuteactualextract+repeatvalidation+manifest/hash/traversalrefusal.41localtestsPASSED+PSAST. CI40966377643 ateff49de33788c2cdc859f5d831b0061e30792bc7 pending. Samecurrentnonce0cfe(privatekeys~3hexpiryfromuserinvocation), no newrunneededbecauseprestageremainedunstarted. NextoperatorcommandResume-CohostSetup.ps1 -ContinueStaged afterCIpasses; itrepairsinterruptedunpackwithouttransfersandthenreadonlypreflights/freshbaselines/approvedsetup. No resource/latency/securitythresholdchanges.

ContinuationCI40966377643 ateff49de33788c2cdc859f5d831b0061e30792bc7 PASSED41testsandexactinternal-network/relay/SSH/PGprotocol+safetyproofs. Logs/hashesretained. ReadyoperatorContinueStaged; no actualnewhostexecutionbyagent.


## 0cfe package/preflights verified; unrelated-site baseline sample failed

UserContinueStaged: BOTHpackagesSHAverified(no uploads/resources), BOTHreadonlyhostpreflightsPASSED, MAINfresh5minbaselinePASSED. DBbaseline thenfailedUnlimitedEnergyhealthyfalseat2.029659s afterAPI9/site8/landing8successfulsamples. OccurredBEFOREstage: no newcontainers/relay/keys/load. This is preservedprotected-sitebaselinefailure, NOT500applicationqualification. No thresholdswerechangedandno newscriptbugdemonstrated.

ImmediateexternalLAPTOPcurl(max2, sameUA): APIHTTP2001.612s,UnlimitedEnergyHTTP2001.282s,landingHTTP2001.586s. Savedqualification/cohost-public-check-after-baseline-timeout.json; thisdifferentvantageDOESNOTproveDBhostpathhealth. NoSSH/SCPprocessleftlocally. Localstagedkeyexpiry20261030012314Z. Safe nextactionONEunchangedContinueStaged retry forfreshDBbaseline/reuseonlystillfreshmainbaseline, preservingfailedattemptrecord; ifsamefailurepersistsstopblindrerunsanddiagnoseDB-hostpublicpath/siteavailability. Do notignoreunrelatedsite,relax2seconds,repeatuploads/newnonce/fullCIorinferproductioncapacity.


## RepeatedDBhostUnlimitedEnergy2secfailure: STOPsetupretryloop

Oneunchangedretryauthorizedbypriorrecommendationalsostopped: BOTHpackages/preflightspassed, MAINbaselinefreshreused, DBUnlimitedEnergyhealthyfalse2.018303safterAPI1/site0/landing0successfulsamples. ThisistheSECONDconsecutiveDBsitebaselinefailure; no containerstage/loadreached. Retainbothfailedattempts; no moreblindContinueStaged retries, nostale-successreusebeyondexisting15minvalidator, no2secrelaxation/siteexclusion.

PreparedCollect-ProtectedSiteTiming.ps1 forONEread-onlyboth-hostcomparison: exactsamecurlTLS/HTTPSredirects/max-time2/UA; API/landingcontrols,3normalUnlimitedEnergyGETsamples,IPv4,IPv6,HTTP1,HEADdiagnosticandcurrent-DNS-pinnedIPv4comparison. EmitsDNSaddresses,exitcodes/errors,DNS/connect/TLS/TTFB/full-downloadtimes,bytes,redirectcount/effectivehost+path(noquery), current0cfemanifestexistence+retainedfailure. HEAD/pinnedvariantsDIAGNOSTICONLYneverqualifybaseline/changeDNS.20smallpublicreadrequestsmaximumacrossbothhosts,nosetup/SQL/settings/ports/load. About30seceachhost,onepasswordeach. PSAST/Pythoncompilepassed. Savedfilesprotected-site-timing-{main,database}.json; awaitoperatorratherthanissuinganotherbaselineattempt. Investigatewhetherdelayisresolution,connection,TLS,originTTFBorfullbodytransfer beforecorrection; don'tassumeapplicationCPUcapacity.


## Sequential path reports healthy; exact concurrent observer comparison required

Read BOTHprotected-site-timingreports fully. MainnormalUnlimitedEnergyGETHTTP2000.125/.074/.051s, DBnormal200.119/.080/.086s; full30,255bytebodyfast. DNS~1-15ms,TLS~40-64ms,TTFB~47-121ms. No redirects. IPv4andHTTP1alsofast. ForcedIPv6couldnotresolvebecausewebsitehasnoAAAA; NORMALGETusedcorrectIPv4successfully. API/landingcontrols200fromboth. Bothsetup_manifest_existsFALSE. No currentpersistentDNS/TLS/body/originfaultproven; prior2secbaselinefailuresremainvalid. DonotclaimlackofAAAAisthecauseorforcefamilyasafixwithoutmatchedproof.

Importantmeasurementmismatch: earliercollectorprewarmedDNS(getent)andranGETsSERIAL, whereasbaseline3sitesCONCURRENT. PreparedCollect-ParallelSiteProbe.ps1 (DBhostONLY,1password,~60sec),36ordinaryGETsatEXISTING3requests/5seccadence,strict2sectotaldeadline,default/IPv4/IPv4/defaultABBAx3,NODNSwarmup. Recordexit/errorandDNS/connect/TLS/TTFB/downloadphasesperrequest. Thisisread-onlyobserverdiagnosticNOTqualification/load;nofixtures/SQL/containerchanges. Outputparallel-site-probe-database.json. Syntax/PSASTpassed. Awaitthatcomparisonbeforechange; no moreblindsetupretries/no2secrelaxation/noIPv4pinningappliedtosafetyprobe.

## Parallel observer passed; actual baseline now retains failure phases

Read parallel-site-probe-database.json: all 36 requests returned HTTP 200 with curl exit 0; maximum 1.119887 seconds. Both default and IPv4 variants passed 18/18. No intermittent failure reproduced and no evidence supports forcing IPv4 or changing network configuration. This one-minute diagnostic does not qualify the five-minute baseline or application capacity.

Added optional per-request curl exit/status/error and DNS/connect/TLS/first-byte/total/download diagnostics to the existing safety probe. Actual baseline appends baseline-observations.jsonl and includes the failed request details in baseline-failure.json. No body, tokens or SQL captured. Same HTTP/TLS, concurrency, five-minute/60-sample, two-second and freshness gates; no retries. Wrapper evidence collection includes observations. Both local current0cfe role tool packages updated; next operator command must use DEFAULT Resume (small package updates and expiry refresh), not ContinueStaged. Images reused, no image transfer. Existing setup-only approval and resource caps unchanged; no load authorization.

Validation: all 43 local safety regressions passed, including timeout phase retention and healthy readiness JSON parsing. Sandbox denied Python temporary-directory access; the identical suite passed outside the filesystem sandbox. No production changes or remote setup executed by agent. Actual next baseline may still fail: retained phase evidence is diagnostic, not a claimed timeout fix.

## Actual diagnostic baseline: healthy API transfer, observer wall-time gate failed

Operator DEFAULT Resume uploaded updated small tools, both readonly preflights passed, MAIN five-minute baseline passed. DB then failed after six successful samples/site on API readiness: HTTP200, curl exit0, valid readiness body, no curl error. Curl DNS0.028581/connect0.066248/TLS0.578833/first-byte1.278149/total1.278215 seconds; Python enclosing wall time2.245950286 seconds. Difference0.967735286 seconds outside curl-reported transfer. Existing two-second enclosing observer gate therefore correctly stopped under its current definition; this is NOT evidence of API transfer timeout. Extra elapsed time is unlocalized (process startup/scheduling/pipe/observer resumption possible); do not assert CPU saturation or a specific cause.

Inspection confirms probe measures from before subprocess.run through output parsing, intentionally includes process overhead; existing test explicitly enforces that. Do NOT switch to curl time_total to manufacture pass, remove protected sites, force IPv4 or blindly rerun setup. No stage reached, no new test containers/keys/load. Preserve failure and discrepancy qualification/cohost-observer-timing-discrepancy.json. Next work must distinguish observer execution delay from network/service delay while retaining the wall-time safety gate; no performance-capacity conclusion is justified.

## Focused observer execution timing and launch decision prepared

Read user-attached ChatGPT instruction. Cannot retrospectively localize prior0.967735s; do not claim exact cause. Implemented Popen launch measurement and communicate/wait measurement separately, plus wait-minus-curl-transfer diagnostic. Preserved original curl2s, communicate2.5s, enclosing wall-time2s and all existing safety/health gates. Timeout explicitly kills/reaps child.45localtestsPASS including launch/wait separation, timeout cleanup, unhealthy/slow/error rejection. Current0cfe BOTHroletoolcopiesupdated; defaultResume required(smalluploads/expiryrefresh,noimages). No host execution possible unattended because SSH needs operator-local password; no newremote action/load/production change. Prepared separate LAUNCH_READINESS_DECISION_2026-10-30.md distinguishing readiness evidence gaps, capacity limitations and deferred changes. Next authorized action actual approved setup with new timing, not another standalone collector; stop with exact phase evidence if gate fails.

## 0cfe actual setup and restricted connectivity PASSED; main cleanup evidence pending

Read full operator attachment82a4dbfb. BOTH five-minute baselines passed, BOTH real test parent cgroups verified. DB loopback-only relay PostgreSQL wire protocol passed. Main13ownedcontainers created (application load not started), restricted tunnel verified actual SQL identity capacity_lab:cohost-0cfe085cbad1ec57; production_database_accessed=false; BOTH restricted-key status calls passed with fresh leases. Setup connectivity milestone achieved. No500capacity/load result claimed.

Final main cleanup SSH closed255 before evidence collection. Do NOT assume independent reaper completion. Database cleanup completed; locally read database-evidence.tar cleanup.complete=1793339226.413023 and load_authorized=false. Main archive absent. Prepared Finish-CohostCleanup.ps1: invokes ONLY existing exact-nonce operator cleanup idempotently, requires cleanup.complete, saves public allowlisted evidence and downloads main archive; no restage/reupload/newnonce/load. Operator-local password needed. Next: inspect main cleanup+verification evidence, then final load plan with separate approval and explicit DBresource-profile decision; setup1CPU/3GiB is not approval for later3CPU test. No production changes.

## BOTH setup archives reviewed: connectivity and cleanup confirmed

User Finish-CohostCleanup succeeded. Read BOTH local tar archives directly: main cleanup.complete1793339374.083846; DB1793339226.413023. Main verification.complete explicitly transport=passed/load_started=false. Both captured watchdog logs report production_healthy=true and no OOM. SHA256/sample counts retained cohost-runtime-cohost-0cfe085cbad1ec57/verified-summary.json. Mainrelay not-loaded warning refers to never-started load gateway relay, not failed database transport; deprecated Docker --time warning nonfatal. No further setup/cleanup retry needed.

Actual approved setup milestone CLOSED successfully. Still no500application qualification. Next authorized local work: adapt/review executable staged load orchestration for proven cohost transport and free generator; exact allocation/lifetime/peer watchdog/fixture-source/ingress/correctness gates must be executable and validated offline before requesting live-host fixture/load permission. Existing QUALIFIED_500_EXECUTION_2026-10-29.md describes older single-runner8GiB/2CPU calibration and is NOT an executable cohost dispatch approval. Do not present it as ready or dispatch it blindly. No new hardware/prod/K3s/500load authorization.

## Executable staged trial prepared offline; no live authorization

Added trial_policy/host/runner, bounded synthetic evidence exchange, registered workflow execute=false default/live true gate, source/proof checks, operator package generator and independent120min reapers. Stage20smoke45s?100stage180s?500gradual180s?original simultaneous500sustained1800s. Earlyworkersoff, finalworkersmocked; queues retained. Original client prewarm SHA matches qualified proof exactly; no application/migration/lab-binary changes. New proposed DBcap3CPU/3GiB requires separate approval; main2CPU/2.5GiB unchanged. Nonsecret review COHOST_TRIAL_EXECUTION_APPROVAL_2026-10-30.md lists full timing, original thresholds, safety and temporary restricted-key repository secret scope. No actual trial dispatch, host changes, purchases or K3s. Local58safety tests+stage-gate mutation regressions+originalNodepolicies+renderer passed; disposableCIvalidation and generatedPowerShellAST still pending before final approval request.

Local final checks now61PASS. Actual generator cgroup sampler/gates preserve historical70%CPU/85%burst/<1%throttle/<70%memory/noOOM/coverage/accounting limits; workload and executed hashes match historical proof. Node original policies/transport/renderer and mutation stage-gate regressions PASS. Existing application/lab source diff vs immutable image13d316c is empty. Registered cohost-preparation workflow reused with execute=false default rather than adding unregistered workflow/default-branch merge. Offline branch push will run disposable checks only; no live dispatch or secret installation permitted until explicit trial approval.

## FINAL APPROVAL CHECKPOINT ? executable cohost trial ready, not dispatched

Exact source dbffabfddb183497a1c796b2871bba3ef11a7226 pushed on experiment/launch-qualification. Final disposableCI41014532150 SUCCESS:63Python safety tests, original renderer/Node stage mutation gates, actual disposable Docker shutdown/owned cleanup and restricted internal PG SSH transport. Live qualification jobSKIPPED. CIrun.json/run.log retained qualification/cohost-integration-20261030/41014532150 with SHA2567064681e0350bf1d20e315610327faedfa0c8065390f6cd07016c744e70b914f and c0b9e674048e53a9512381e50061a26b23997adaaad733f5f0e405a24c613151.

Prepared final fresh nonce cohost-0c667adb06298ad8. Current review pointer qualification/cohost-trial-review-latest.txt; all permission flagsFALSE. Final operator qualification/Invoke-CohostQualification.ps1 ASTPASS/SHA25678fefc462c9da69fbd9c6856996b6b134a71a42308b615473d8ea193ac81d55e. Supersedes locally prepared uninstalled/nonexecuted032f57/b0ee packages; none contactedVPS. Full review COHOST_TRIAL_EXECUTION_APPROVAL_2026-10-30.md; record COHOST_TRIAL_FINAL_VALIDATION.json. Separate explicit approval required for synthetic seeding, main2CPU/2.5GiB, DB3CPU/3GiB(up fromsetup1CPU), existing freeCI2CPU/4GiB, bounded120min, temporary restricted-key repository secretCOHOST_TRIAL_BUNDLE(add/remove), stage20?100?500gradual?500simultaneous30min, no1k/prod/purchases/K3s. Expected75?110min plusbaseline/passwords;65minworker/mock remainsboundedand expiry fails gate. Original generator70/85%CPU/<1%throttle/<70%RAM/noOOM/accounting criteria retained onACTUALtest.

After USERAPPROVAL ONLY, operator command: & "C:\Users\EMMA\Desktop\gouwu\qualification\Invoke-CohostQualification.ps1" -ApproveTrial. Wrapper refreshes uninstalled3h keyexpiry, reuses remoteimages(no199MiBtransfer), createsfreshownedresources, validatesfreshbaselines/tunnel, installsoneephemeralrepoSecret, dispatchesregisteredcohost-preparationexecute=true exactnonce/sourceproof, watches/downloads, deletessecret, exactownedcleanup+evidence. Current trial flagsFALSE/nohost changes/noGHlive dispatch. Do not run oldResume onclosed0cfe. Frozen approval checks remoteHEAD=dbffabf; leave codeHEAD until trial runs. Latest report/handover checkpoint mirrors are intentionally local documentation changes after source freeze, to commit after the approval-bound execution; do not manufacture a new safety proof for them.

## Approved trial0c667 stopped before verify: SSH transport failure, cleanup confirmed

User explicitly executed Invoke-CohostQualification.ps1 -ApproveTrial, authorizing the reviewed staged load/fixture/3CPU DB/temporary restricted-key secret envelope. Approval persists; do not ask again for unchanged scoped recovery. Both baselines/preflights/stage cgroups passed. Next password-authenticated MAIN verify SSH closed255 before dbtunnel/verification marker existed. NOload/seed/CItrialdispatch happened. Both downloaded archives directly reviewed: maincleanup1793343366.4960978, DBcleanup1793343405.0790002. Both exact key lines removed. No active attempt to resume. This is setup SSH failure, not500capacity result; precise SSH close cause is not available from transcript.

Focused correction: forced-key endpoint setup-verify permits ONLY exact root-run directory/main role/loadfalse and delegates existing operator verification/approval/ownership/cleanup code. Generated qualification wrapper now uses already-installed ci-main key with BatchMode/strict host checking for this handoff instead of another password-authenticated root command. No unrestricted shell/key or sshd policy change.64localtestsPASS. Local docs now include explicit approval and previous final checkpoints; code must pass disposableCI before replacement package. Next is same already-authorized trial via regenerated fresh nonce (old was cleanly closed), no additional spending/limits/workload/threshold changes.

## Scoped-key recovery package ready for approved retry

DisposableCI41017376689 passed64tests+realDockerownedcleanup/internalSSHprotocol at26f05f9affe235c1cf4890e630c1a8bf99262608; qualification jobSKIPPED. Regenerated fresh localnonce cohost-5dd66295a271c215; nevercontactedservers/installedkeys/secret/dispatchedtrial. Operatorqualification/Invoke-CohostQualification.ps1 ASTPASS and exactlineuses ci-mainBatchMode setup-verify, nointeractivepasswordverificationhandoff. COHOST_TRIAL_FINAL_VALIDATION.json updated. Current permission JSONflagsstayfalseuntiloperatorruns -ApproveTrial, but user's existing approval for unchanged trial scope persists; this is not a new approval request. Old0c667cleanupbothconfirmed; neverrestageclosednonce. Next user-localcommand SAME Invoke-CohostQualification.ps1 -ApproveTrial; only smalltools uploads/imagesreused. RootSSHpassword neededforinitialstaging; laterverificationusesexistingrestrictedkey. Noactual500loadattemptorcapacityresultyet.

## Latest approved retry stopped MAIN baseline: timing-policy decision, no blind retry

Userranupdated26f05f9trialwrapper5dd66295a271c215. Bothsmalluploads/SHA/preflightspassed; MAINsampleafter21success/siteHTTP200/curl0/readinesshealthy:transfer1.731229s,launch.058297s,wait2.199113s,enclosing2.257508s,waitoutsidecurl.467884s. Original2s enclosinggatefailedcorrectly. StoppedBEFOREstage/fixture/secret/CIload. No testcontainers/keyscreatedbythisattempt. Exactoverheadcause(stall/childteardown/pipe/scheduling)notlocalizedandnotclaimed. Repeated unchanged retries are not nextaction. Prepared BASELINE_TIMING_POLICY_REVIEW_2026-10-30.md with explicit proposed2sHTTP+2.5sindependentobserver guard,replaycasesand unchangedapp500/1500ms/error/integritychecks. ProposalNOTAPPLIED; conflictswithexisting2senclosingrulethereforeUSERAPPROVALREQUIREDbeforeimplementation. Earliertrialapprovalpersistsforresource/loadscopebutdoesNOTauthorizeweakeningthisrule. Currentrootreviewpointerstill5dd; no update/upload necessaryuntilpolicydecision. Retainoriginalfailedbaseline/evidence; do not claimnewpolicypassesoldthreshold.

## Explicit timing-policy approval implemented; resource/workload authorization unchanged
UserapprovedHTTP<=2s and observer<=2.5s split andcontinuedpreviousstagedtrialscope. Narrowsharedprobechange returnsHTTPseconds forbaseline/runtime latency while recordingobserverseconds separately andfailshealthonwall>2.5; curlmax2 maintained, processcommunicatebudgetsubtractslaunch. Baselineversion http-2s-observer-2.5s-v1 andseparateobserverp95required; neverreinterpretoldbaselineevidence. Trialapprovalmetadatarequiresnewpolicyversion.68localtestsPASS incllatest1.731HTTP/2.258wallacceptedunderNEWrule,HTTP>2/wall>2.5/unhealthy/malformed/nonfiniteerrorsrejected. Existing realshutdown cases extended forHTTPtimeout/observerstallhealthfailures. PreparingdisposableCI; no livehostchanges/seed/load. Scope remainsmain2CPU/2.5GiB,testDB3CPU/3GiB,freeCI2CPU/4GiB,120minhardbound,no1k/prod/purchase/K3s. Existing5ddattemptPRESTAGEonly; no containers/keysactive. Next operatorpackageafterCIpasses, sameapprovedcommand/noadditionalpermissionneeded.

## Approved timing-split retry READY: CIpassed68tests, operatorupdated

Source849f9c896366ffdab2b9559b50f295dc07482c66; disposableCI41019279690 SUCCESS (68Python tests, Node threshold/renderer regressions, actual Docker watchdog shutdown inclHTTPtimeout/observerstall healthcases, ownedcleanup and internalSSHtransport). NoVPStrafficbyCI/livequalificationjobnotrun. Metadata/logretained qualification/cohost-integration-20261030/41019279690. Newlocalnonce cohost-fd5d24acae10b453; nohostresourcescreated/noinstalledkeys/secret/seed/liveworkflow. OperatorASTPASS/SHA2568dd59206977e717926c47446d6f6a5a51711ef470d53b2def2f7f3ca576a0ced. Includesrestricted-keysetup-verifyhandoffandexplicitapproved http-2s-observer-2.5s-v1 baselines/guard. ExistingtrialapprovalANDnewtimingapprovalpersist; noadditionalconfirmationneeded. Samecommand Invoke-CohostQualification.ps1 -ApproveTrial requiresuserlocalSSHpasswords. Noimagesreupload. Capacityresultstillpendingactualexecution. CurrentreviewpointerandCOHOST_TRIAL_FINAL_VALIDATION.jsonupdated; sourceHEADfreezeat849untilapprovedexecution.

## fd5d initial unpack SSH stalled before any stage/load
Userwaited>5minafterMAIN241KiBupload. ReadonlylocalCIMconfirmedONLYsshPID7472invokingexactfd5d tar/chmod/rm command; no otherSSH/SCPprocess. Cannotinferauthenticationcompletedorremotetarstartfromclientcommandline. No baseline/preflight/container/fixture/secret/CItrialstep reached in user transcript. Added45sremote timeoutaroundtarandchmodinLOCALoperatorwrapper ONLY; actual trial source849/proof/profile/approvedtiming unchanged. Updated operatorSHArecord. UsermustCtrl+Ccurrentcall, waitPSreturn, rerunsameupdatedInvoke-CohostQualification -ApproveTrial; defaultprestageguardsrefuseanymanifestandallimagesreused. Do not start overlappingwrapper. No newapprovalneeded. Transporttimeoutboundsactualcommandsafterauthentication; itdoesnotclaimtoresolveunknownSSH/network/authenticationcause.

## Precise post-heartbeat blocker isolated: PowerShell5 credential serialization; stop/audit, no duplicate setup
Userrequestedimmediateinvestigationwithoutsetupretry. LocalCIM: operatorPowerShellPID8976 exists/noSSH/SCP/gh/ssh-keygenchild (only our ownreadonlydiagnosticghprocess). GitHublatest8runsallofflinepush/completed; no qualificationdispatch/queuedrun. RepositorysecretlistEMPTY: COHOST_TRIAL_BUNDLEneveruploaded. Exactnextstepsafterstatusare mainhostkeylookup?credentialbundlecreation?ConvertTo-Json Depth20?ghsecretset?sourcecheck?workflowdispatch.

Rootcauseproof: PowerShell5 Get-Content -Raw strings carryPSPath/PSParentPath/PSChildName/PSDrive/PSProvider/ReadCountETSmetadata. BoundedPUBLICfile reproduction using sameDepth20 serialization exceeded3secondsandonlythatownedproofchildwaskilled; plainReadAllText stringserialization309ms. CorrectedACTUALprivatebundle locally usesReadAllText forci-main/ci-database/database_known_hosts;serialized20ms,2161chars,keysroundtripasstrings; no keyvaluesprinted/filewritten/uploaded. Patchedcurrentlocaloperatorandpackagergenerator; no runtime/threshold/hostchanges. Codefixnotyetcommittedbecauseapprovedcurrentattemptmustfirstbecleanlyclosed; no newnonce/package/dispatch.

BOTHinstalledrestrictedkeysnowdenied; cannotassertwatchdogactiveorresourcesstillrunning. Existing14minsetupreaperlikelyrevokedkeysbutNOTconfirmed. PubliclaptopGETsAPIreadyvalidoktrue/UnlimitedEnergy/ATLlandingallHTTP200 (~3.1?3.6sfromlaptop,notbaselineproof). TolduserCtrl+C ONCE andfinishexistingfinallycleanup passwordprompts; doNOTresumeoldserializationASTorstartsecondwrapper. AfterPSreturnsrunqualification/Verify-ApprovedTrialCleanup.ps1 (newreadonlybothhostsummaryscript,noSQL/load/startup) toconfirmexactfd5dcleanupmarker/zeroownedcontainers/zeroexactkeylines/unitstates/productioncontainerhealth+publicprobes. SSHpasswordneededbecausekeysnotaccepted; no renewedpermissionrequested. Ifresourceauditnonzero, performexistingexactnonceoperatorcleanup, notsetup. No actual500applicationtrafficstarted; currentlynotqualificationcapacityfailure.

## fd5d controlled cleanup and production health VERIFIED; serializer regression fixed
Readbothapproved-trial-cleanupJSONsfromuser. BOTHcleanup_markertrue/remaining_test_containers0/remaining_test_key_lines0; allreaper/relay/tunnelunitsinactive; historicalguardunitsfailedbutNOTactive. Main16productioncontainershealthy/DB3healthy, publicAPI+UnlimitedEnergy+ATLHTTPhealthyfrombothhosts (main.446/.092/.346s,DB.249/.161/.218s). No resourcesremain to resume. No duplicate setup orloadwasstartedbyagent.

Committed-nextcandidatepackagerfix usesIO.File.ReadAllTextforbothprivatekeys/pinnedhoststring, JSONDepth8, validatesdecodedkeystrings+<48000chars, explicitserialize/uploadprogress. Persists45suntar/chmodbounds. Newtest executesACTUALPowerShellcredential-buildingandvalidationtextwithdummyfiles, timeout10s:PASS1.22s; existing68PythonchecksPASS (69totalincludingnew). Actualbundleearlier20ms/2161chars, no realsecretupload. Source runtime/core/application/threshold/limits unchanged. Next publishfix/disposableCI thenfreshapprovedtrialpackage; onlynewtestresourcesneedrecreatingbecauseoldrunisconfirmedcleaned, existingimagesreused. Existinguserload/resource/timingapprovalspersists; no newconfirmationrequest.
