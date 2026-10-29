# ATL Xpress master handover

Updated 29 October 2026 (Africa/Lagos). This is the current entry point for a new conversation. It supersedes stale NEXT/ACTIVE statements in historical HANDOVER.md, not their retained evidence. All statuses are evidence-specific; a passing isolated test does not establish production capacity.

## Start here

Workspace: `C:/Users/EMMA/Desktop/gouwu` (the workspace root itself is NOT a Git repository). Read this file, then inspect the exact worktrees below. Do not replay old operator deployment/migration commands from chat or archived handovers. No production operation or server purchase is currently authorized.

Immediate sequence: finish notification-batch regression and matched benchmark; only if both pass, run prepared long independent generator qualification; establish honest isolated database/resource headroom; run bounded actual-stack calibration with unchanged gates; only after prerequisites pass, unchanged 500-session sustained qualification. K3s preparation runs separately. Stop escalation on a failed prerequisite and preserve artifacts.

## Product, repositories and ownership

ATL Xpress is a Nigerian marketplace with customer/provider accounts, products and services, provider subscriptions/payment records, Flutterwave checkout, Town Hall requests/bids, direct/community chat, support, admin roles, notifications, and nearby passenger drivers/couriers with leased location presence.

- Backend: `https://github.com/3my1234/across-backend-api`, Go/Fiber, PostgreSQL, Redis, API and separate worker processes; durable SQL notification/outbox records and push receipts. Relevant paths: `internal/controllers`, `internal/services`, `cmd/api`, `cmd/worker`, `cmd/capacity-lab`, `deploy/session-lab`, `deploy/notification-lab`, `.github/workflows`.
- Mobile: `https://github.com/3my1234/across-mobile-app`, React Native/Expo SDK54, TypeScript, Expo Image, Android EAS preview APKs. Mobile worktree below. Backend optimization is not mobile code.
- Web: `https://github.com/3my1234/across-admin-panel`, provider PWA and separate admin resource. Provider automatic deployment was disabled; admin automatic deployment was enabled in last operator inventory. Do not assume those historical settings still hold before a future release.
- Redis supports shared runtime state; exact persistence/recovery and dependency inventory must be completed before any migration. PostgreSQL remains authoritative for business data.

## Current infrastructure and approved K3s direction

Two existing independent Contabo EU VPS hosts, not nested VMs:

1. Main/application host `173.212.219.169`: Coolify, original API, two API replica slots, gateway, worker, Redis, admin and provider web resources. Also an unrelated website which MUST stay online under Coolify. Never repurpose this host as a K3s node by assumption.
2. Database host `79.143.176.173`: dedicated ATL Xpress PostgreSQL (`across_db`), registered as a remote server in Coolify, private database connectivity. No production load test or reset.

Reported production DB sizing is approximately four vCPUs/eight GiB. Fresh CPU model, allocated RAM, filesystem/IOPS/latency, disk headroom, network RTT, exact DB settings and failure domains are NOT verified by that description. No speculative capacity/cost claim.

User intends THREE additional independent VPS hosts shortly. Target is FIVE hosts total: existing main/Coolify + unrelated website; existing external PostgreSQL; three NEW K3s server/application nodes with embedded etcd. Do not purchase them or assume arrival. No K3s installation on either existing host. Three nodes do not make external PostgreSQL, Redis, external ingress or the entire platform highly available. Confirm hardware, private networking and real failure domains before installation.

The October28 `K3S_INFRASTRUCTURE_DECISION_2026-10-28.md` four-host/reuse-main option is SUPERSEDED by the five-host target. Keep it only as historical alternatives. K3s work currently has reserved placeholders and must fail closed before live use. New package status/commit is recorded in the live checkpoint below.

## Exact worktrees and source state

| Worktree under workspace | Branch | Source/status |
|---|---|---|
| `.worktrees/launch-qualification` | `experiment/launch-qualification` | Notification batching candidate `d5a854c2d00914b5db944f3f8bae8659599df134`, pushed; NOT merged/deployed. Includes prior nearby/inbox/chat/worker experiments. Inspect `git status --short`; never overwrite another worktree. |
| `.worktrees/generator-headroom` | `experiment/generator-headroom` | Prepared long generator phase `ac854e5`; original short proof source `41ccc8344a6974d43126b96343746bc4f4ed76d1`. No application-source changes. Dispatch held until batch correctness AND benchmark pass. |
| `.worktrees/mobile-login-overload` | `experiment/media-google-promoted-banners` | APK63 application source `9758da7`; subsequent fixture-only commit `40c686224597b69ba5e4311ae6960cafe30d30a4`; PR60 merged to mobile main `ee7546c6997d70494dedd8edd21f22a46803c82f`. |
| `.worktrees/inbox-performance` | `perf/inbox-page-work` | Historical inbox differential/source proof `67245f4`; preserve evidence. |
| `.worktrees/notification-workers` | `perf/receipt-throughput` | Receipt throughput/lease recovery source `d72df8b1f205886f9b03db889c79d5359d9628ec`; experimental migration108, NOT production schema. |
| `.worktrees/expanded-capacity-ramp` | `experiment/expanded-capacity-ramp` | Historical expanded workload/importer/failure evidence; do not dispatch obsolete branch blind. |

Get authoritative local inventory with `git worktree list --porcelain` from the backend worktree and `git status --short` in each active tree. Check the live checkpoint for later commits; hashes above identify measured source, not necessarily latest documentation HEAD. No mobile changes belong in backend qualification PRs. No backend experimental branch has approval for main merge or production deployment.

## Verified mobile release checkpoint

User installed/tested APK63 and accepted its appearance, then explicitly requested applying APK changes independently of scaling. Mobile PR60: https://github.com/3my1234/across-mobile-app/pull/60 (MERGED).

- Version63 EAS build `5a4ba444-89cf-461a-80b8-7d67b295e378`, source9758da7, FINISHED.
- APK: https://expo.dev/artifacts/eas/cKMDrsdssKWTfhqfBTHxxBhIykvaezxVPlO9k-xjy7c.apk
- Ads now listing-proportioned photo frames with captions, preserved carousel/dots/pausing. Services prioritize visible first images and limit render batches. Stable canonical disk cache remains; private images remain memory-only. No measured phone loading speedup claim.
- Shared gallery fallback, Google handoff and bounded login overload fixes are INCLUDED in that APK/merge. Registration is NOT automatically retried; password hashing/admission unchanged.
- All49 native provider suites, targeted gallery/Google/login/ads/cache/recovery checks passed. CI launch40842461051, discovery40842461066, native Android40842461077 and postmerge launch40842584350 all SUCCESS (verified October29).
- `--auto` merged immediately rather than gating on all checks; remaining checks subsequently passed. Do not repeat that assumption; inspect required branch protections before a future merge.
- User's report is visual/device acceptance of tested APK, not a report that every gallery/auth/device/network scenario was exercised. No new APK/store publication is needed simply for the fixture-only merge. Retain broader device acceptance limitations.

Evidence: local `qualification/ANDROID_WIDE_ADS_BUILD_2026-10-29.md`, `ANDROID_ADS_GALLERY_ACCEPTANCE_2026-10-28.md`. Earlier version60/61/62 layouts were superseded; do not release those by mistake.

## Performance history: preserve outcomes and scope

Important raw reports live under workspace `qualification/`; tracked backend documentation is separate. Full CI run URLs use `https://github.com/3my1234/across-backend-api/actions/runs/<run-id>`.

| Evidence | Finding and limitation |
|---|---|
| Original chat-history isolated benchmark | Reported ~55 to460 requests/s (~8.37x), p95~740 to80ms; query-only, NOT full-platform throughput. Indexed bounded branch merge and old-page/prepared-plan regressions retained. |
| Matched mixed workload | Reported ~38% throughput improvement with reduced PG CPU; not independent-session production capacity. Retained earlier reports/evidence. |
| Initial 100 session stage | Login burst admission503 prevented activity qualification. Corrected test serially authenticates distinct users before measured workload, retaining protections. |
| Initial 100 sessions/30min HTTP-only | 80,898requests/44.85RPS, worst operation p95~48.14ms/p99~58.37ms,12,125ACKstoredonce. Workers off; queue growth not failed delivery. |
| Receipt qualification40627566223 | 100session sustained with actual workers/mock transports; 81,186requests,11,995ACKstoredonce; send/receipt recovery/drain proof. See RECEIPT_CAPACITY_RESULTS_2026-10-28.md. Real15min receipt eligibility preserved. |
| Expanded sustained10040636465564 | 90,152requests,zero HTTPerrors/overload/drops,14,372ACKstoredonce; Town Hall bids,487GPSupdates,120offline,3036nearby; mockqueues/receipts drain. Different runner hardware limits unmatched comparisons. See EXPANDED_SUSTAINED_RESULTS_2026-10-28.md. |
| 500importer timeout40688491116 | Fixture preparation failed before measurement; NOT500capacityfailure. Named historical trigger importer correction separately tested; real triggers restored. |
| Repeated 500bursts | All500sessionsauthenticated, then overload and pool waits; earlier569 and1243HTTP503/p95610and959ms. About2s aborted windows, NOT sustained500 capacity. ACKpreserved; unconfirmed commits require idempotent retry accounting. |
| Nearby corrected comparison40741981937 | Ordered cheap geo candidates then lazy entitlement checks, no prelimit; full ordered equality including revocations. ~1.33?1.86x query-only throughput. No RedisGEO/pool/admission change. Actual handler/privacy/expiry/docs tests40746907768PASS. |
| Independent generator40747865966 | 500sessions/180s,46,683offered=completed,~254.89RPS, no drops/throttle/OOM; initial CPU p95~49.84% of2CPU after local prep vs~97.95% previous. Exact real cached auth bindings and traffic preserved. Not30min proof. |
| Actual stack calibration40750457084 | At2d350ae, aborted1.863s:1253offers/1127responses/753HTTP503; successful p95~716.79ms. Generator p95~44.9%of2CPU/no throttle/OOM in captured shortwindow. PG2CPU saturated,385memory.maxevents/~1GiB ceiling.43ACKallstoredonce,52unconfirmedcommits, no duplicates. Full500unqualified. |

The retained calibration SQL window includes authentication/burst/recovery. Notification INSERT600calls/2625ms is cumulative elapsed SQL time, NOT exclusive CPU attribution. Worker SQL was~3.5% retained elapsed; notifications are an evidence-supported API fanout target, not a guarantee batching alone fixes capacity.

Reports: `ACTUAL_STACK_CALIBRATION_RESULTS_2026-10-28.md`, `NEARBY_ORDERED_ELIGIBILITY_RESULTS_2026-10-28.md`, `GENERATOR_HEADROOM_RESULTS_2026-10-28.md`, `GENERATOR_SUSTAINED_PREPARATION_2026-10-29.md`, `ISOLATED_DATABASE_RESOURCE_MATRIX_2026-10-29.md`, `SESSION_AND_AUTH_CAPACITY_RESULTS_2026-10-27.md` (tracked backend docs), receipt and expanded reports above. Raw latest calibration: `qualification/actual-stack-calibration-20261028/40750457084/`; generator: `qualification/generator-headroom-20261028/40747865966/`.

## Current notification batching implementation

Code: `internal/controllers/community_controller.go`, `community_fanout_test.go`, `community_fanout_capacity_test.go` on launch-qualification.

- Replace recipient SELECT + per-recipient INSERT loop with bounded64-recipient ordered CTE INSERT SELECT, inside caller's EXISTING business transaction.
- Preserve active recipients, self-exclusion, both directions of social blocks, generic private preview, metadata, per-recipient event key and ONCONFLICT dedup.
- Cursor progresses by SELECTED recipient UUID, not inserted count, so completely duplicate pages cannot hide later new recipients.
- Normal Town Hall room population fits one batch. No new unbounded Go recipient slice, independent commit, external send or pool/admission adjustment.
- Tests: private payload/recipient filtering and rollback;140recipients crossing3pages; four concurrent retries; later recipients after duplicate-only pages; final-page injected error rolls back earlier notifications, business message and row-trigger enqueue probe.
- Matched ABBA benchmark: same10recipientfixture,100transactions/arm, original helper retained test-only, row-trigger probe, real pg_stat_statements; expected1100vs100fanoutSQLcalls. Counts and SQLexecution timings are NOT results until CI succeeds.
- Row-trigger probe is not actual worker recovery. Separate receipt lease/recovery tests run in same CI; full real transport/trigger chain remains application qualification responsibility.
- Fanout CI40853697871 atd5a854c is RUNNING at initial checkpoint. Inspect actual outcome/artifact before long generator dispatch. No new migration introduced by batching; candidate already contains experimental receipt migration108 inherited from previous work.

## Test environment qualification and resource constraints

Existing approved resource is standard public GitHub runner (~4vCPU/16GiB, actual hardware varies). Prior PG2CPU/1GiB is smaller than reported production. No production destructive test.

Prepared long independent generator phase on `experiment/generator-headroom`:

`gh workflow run worker-role-qualification.yml --repo 3my1234/across-backend-api --ref experiment/generator-headroom -f phase=generator-sustained`

DO NOT dispatch until batch correctness AND matchedbenchmarkPASS. Sourceac854e5, mock-only atomic messageID racefix. Same500identity client hashes as shortproof; existing1800s profile. Gates>=1795smeasurement/>=1790sCPUcoverage/>=1790snapshots500VU; p95CPU<70%quota/initial6s<85%,<1%throttledperiods,memory<70%2GiB,noOOM/drops/accountingloss. No passwords/production traffic. This validates generator against synthetictarget, not app capacity.

Resource matrix: keep optimized source/data/settings/workload fixed, compare PG1GiBvs8GiB at SAME2CPU; generator2CPU/2GiB. This isolates RAM only. Do not claim four-production-DB-CPU equivalence from a shared4corehost; API/worker/gateway/Redis also need CPU. Observe host idle/runqueue/steal/swap, cgroupCPU/throttle/memory/OOM, DBIO/queries/locks/pools, perendpointlatency andqueueage. If headroom inadequate, preserve evidence/propose an available separately isolated environment; no paid runner or server purchase without approval. Fresh production I/O andnetworkinventory remains unknown.

Do not add new workloads to the unchanged primary500benchmark and call it comparable. Document tested/excluded journeys separately (media transfer/authburst/workerrealexternaldelivery); additional coverage must be separately named/gated. Do not progress1000withoutsuccessful500anduserreview/approval.

## Production state, safety and unresolved launch items

Last operator evidence October28, NOT a fresh October29 inventory:

- Main18:46UTC: API/2replicas/workerhealthy,restart0/noOOM; API+workerproductionflags passed; timeractive/serviceinactive. Backend image source30b0b68. Admin/provider068ff62. Earlier string image names changed IDs after runtime reconciliation; don't infer commit from sha alone.
- Publicreadiness403 occurred with default PythonUserAgent; descriptive readonlyUserAgent yielded200/all8checks. Collector now identifies stage/status safely. No route/securitybypasschange.
- DB18:49UTC: schema107, email/nativepush/browserpending0 in one snapshot (not drainrateproof). Latestarchive558545bytes,mtime18:17:17.534887UTC,sha256 `a7c59da4945377249200266d4227e3a56a1c22eed4497dce597eaf2ee4e96c87`;inventory/fulldecodePASS. CoolifybackupSuccess18:17:31UTCand`atlxpres-db-backup-storage` listed.
- Localarchive validation is NOT offserverdownload/restore/PITR or measuredRPO/RTO. Externalalertreceipt/oncall/firewall/security/productiontrafficlimit/currentrollback remain incomplete.
- Historical private rollback captures remain on hosts; latest checkout/media backend capture `/root/atlxpres-release-rollback/20261026T092109Z-573ce9bb`; webcapture `/root/atlxpres-checkout-media-web-rollback/20261026T100016Z-f4c1e455`. They are historical and not a fresh release-matched guarantee. Never paste their private contents.
- PHP security warning mentioned by user/advisory is UNRESOLVED. Exact advisory/CVE/package/version and affected unrelated site ownership are not established here. Do not claim patched or infer a CVE. Locate original evidence before a scoped remediation.
- No defensible numeric production traffic ceiling. Passed100isolatedsessions !=100productioncapacity, and500isnotqualified. No broadlaunchreadinessclaim.

Production changes requiring explicit confirmation: backend/mainmerge, migration108(oranyproductionmigration), deploy/restart, K3sinstallation, DNS/ingress/firewall/VPN/proxy, Redis migration, database restore/failover, secret/billing changes, new compute/purchase, trafficcutover. No real email/push/payment sends in lab. Keep original/admission/pool/securitygates. Do not send messages to third parties. Existing mobilemergeapproval was specific to acceptedAPK, not backendreleaseauthorization.

## K3s package acceptance checklist

Prepare working local/CI configuration, not a conceptual-only diagram; keep it on its own branch. Pin image digests and reviewed K3s version at actual rollout, no live placeholders or embedded credentials. API probes must distinguish health/readiness and allow startup/drain; grace period must exceed current3sdrain+65sshutdown. Disable inline migrations/workers; standalone migration job explicitapproval and verifiedschema. Sum DBconnectionbudgets across normal/surge/migrator/worker/observer, including readpool aliases; never let HPA multiply an unbounded budget.

Staging must use isolated DB/Redis/objectstorage/mocktransports. Existing worker transport URLs must be checked; defaultworkerreplicas0anddeniedexternalnetwork until full mockegress configured. Namespace networkpolicy alone needs supported enforcingCNI and host/firewall validation. Three etcdvoters require privateconnectivity/TLS/encryption/secrets/backup/restore/failuredomainproof. HAingress/loadbalancer is separate fromcontrolplanequorum. Do not reuse productioncredentials in syntheticstaging.

Validate manifest structure + Kubernetes API schema/rendering; local/CI dryrun is NOT live install/failoverproof. Prepare approval-gated deployworkflow with noautomaticproductiontrigger; CIcannotassumeGitHubEnvironmentprotection exists. Productionproductionapproval must be failclosed/readable before changing clusters. Stagefunctionaljourneys,rollingrollback,nodefailure,backuprestore,monitoringalertdelivery require futureisolatedcluster.

## Ordered continuation

Immediate: inspect actualGitstates and checkpoint; reviewfanoutCIoutcome; fixonlyspecificfailures; retainABBA/equality/rollback/workerproof; launchlonggeneratoronlyafterpass; comparecorrectsoftwarewithcontrolledDBRAMandsharedhostmetrics; boundedcalibration; unchanged500sustainedonlyifallgatespass; archiveandwriteactualfindings.

Inparallel: completeK3spackage/separatebranchwithvalidation; inventorydependenciesandmissingproductionmetadataread-only; keepdocumentationcurrentandallunknownsexplicit.

Awaitingservers: verify threenewhostspecs/storage/network/failuredomains; obtain explicitinstallapproval; configureisolatedclusterandstagingdependencies; validateHA/rollout/rollback/alerts/restore; proposecutoverforapproval. ExistingCoolify/unrelatedwebsiteandexternalPGmustremainuntouched.

Awaitingapproval: purchases/prodchanges/1000stage/prodcutover/securityremediationwithscope. Optionalfuture: thumbnail/CDNdeliveryforpubliclistingphotos, DBreplication/failover,RedisHA,horizontalautoscalingonlyaftermeasurements.

## Live checkpoint

Initial current checkpoint: batch code/testsd5a854c pushed; fanout40853697871 running; generatorlongstagepreparednotdispatched; K3swork delegated toexistingagent onaseparateworktree (exactbranchpending). Root owns qualification/masterhandover. Continue fromactualCIstate, neverassume backgroundprocessesstillrunning afterbranch.

## Branch starter message

Read `C:/Users/EMMA/Desktop/gouwu/qualification/ATL_XPRESS_MASTER_HANDOVER.md` first, then its live checkpoint and `git status` in `.worktrees/launch-qualification`. Continue notification batching/ABBA at recorded source, qualify the prepared30minute500VUgenerator and honestisolatedDBresources, then run the unchanged500stage only after prerequisites pass. MobileAPK63 changes are accepted and already merged via PR60; do not entanglethem. Finish the separateK3spackage for existingCoolify+externalPG plus threefutureK3snodes, without installing on currentservers. Preserve evidence, production/unrelatedwebsite, secrets and all approvalgates; no purchases, prodmerge/migrations/deploy/routing/Redischange or1000stage by assumption. Update this handover with verified outcomes rather than restart diagnostics.

### Verified update: notification comparison and skipped worker correction

Fanout40853697871: controller correctness and ABBA benchmark PASS atd5a854c. SQL1100->100calls, mean118.675->72.365ms (~39.02% less), controller~2.58x throughput, shortsequentialmicrobenchmark only. Artifact review found workerlease tests SKIPPED due missing RECEIPT_TEST_LOCAL. Correctedworkflow29ee9fa/run40854663425 is running and explicitlyrejectsrequiredskips. Longgenerator remainsHELD. Read NOTIFICATION_BATCHING_RESULTS_2026-10-29.md; do not mistake greenoverall40853697871 for recoveryproof.

### K3s package verified and pushed

Separate worktree `.worktrees/k3s-readiness`, branch `experiment/k3s-preparation`, HEAD `084717875d5a79356b3dd6e6e5471411814ec94d`; implementation8da2613. Based on historical experimentaleb9ffd1, NOT a production application release. Select deployment-only changes for any future integration; do not merge that entire historical app base blindly.

Files: deploy/k3s/staging.yaml, README.md, server-config.example.yaml, validate.cjs, validate.test.cjs; .github/workflows/k3s-preparation.yml; docs/K3S_PREPARATION_HANDOVER_2026-10-29.md. Strict kubeconformv0.7.0 validation using Kubernetes1.35.0 schema passed13/13resources,0errors/invalid/skipped. Ten offline policy/mutation/source-contract tests passed. This schema baseline is NOT a chosen productionK3sversion. No cluster was contacted. Release guard intentionally fails placeholders; no live deploy path.

Three-new-node target retains existing Coolify/website and external PostgreSQL. Hard host anti-affinity with zero-surge/one-unavailable rollout avoids a fourth-node requirement at the3podcap. Worker remains stopped and egress denied pending actual transport/role qualification. Missing reviewers on GitHubEnvironment would not enforce humanapproval; configuration remains an explicit future gate. No purchase/install/prodDNS/firewall/Redis/credential change.

Read `qualification/K3S_PREPARATION_HANDOVER_2026-10-29.md` for exact remaining network, hardware, HA ingress, storage, worker, restore, monitoring and PHP-warning unknowns. Production readiness remains incomplete.

### Current execution checkpoint ? no-skip proof and long generator

Batch implementation source d5a854c is now qualified by corrected CI40854663425 at29ee9fa: all controller, privacy, batch-boundary, concurrent retry, final-page rollback and actual PostgreSQL receipt lease/recovery tests executed/PASS with no skips. Two matched ABBA runs retained; SQL1100->100calls, cumulativeSQL29?39% lower, controller2.37?2.58x only. Read NOTIFICATION_BATCHING_RESULTS_2026-10-29.md and both raw artifact directories.

Long independent synthetic-generator run https://github.com/3my1234/across-backend-api/actions/runs/40855200111 is RUNNING at exactsource ac854e5b597a564c96ed2bbddda7d14315261d74; created10:08:44UTC/11:08:44Lagos. Local/race/hash gates passed; thirty-minute measurement step active. See GENERATOR_SUSTAINED_RESULTS_2026-10-29.md for the agent's latest report. No actual application500 or1000 has started in this continuation.

RAM-sized actual-stack calibration preparation source844af84 on experiment/launch-qualification is pushed and NOT dispatched. Workflow phasecalibration now REQUIRES explicit generator_run, passed1800secondgate artifact and no internal/migration/API/session workload changes versus29ee9fa. PostgreSQL RAM choice defaults8g, optional1g control; DB2CPU/gen2CPU2GiB/application limits unchanged. Host memory gate12GiB available for8g. cgroup samples now retain memory.max, blockIOcounters and timestampedhostCPUjiffies; no production telemetry access. Local YAML/Bash/Python/JS/policy/transport checks passed.

NEXT only after40855200111 concludesSUCCESS and its completeartifact is independently reviewed: `gh workflow run worker-role-qualification.yml --repo 3my1234/across-backend-api --ref experiment/launch-qualification -f phase=calibration -f generator_run=40855200111 -f calibration_memory=8g`. This is bounded180s calibration, NOT full500sustained; do not auto-escalate. Actual shared-host/generator/DB/correctness/headroom gates must pass before proposing unchanged30minute500 qualification. NoRAM-gain attribution without matchedresourcecontrol;8GiB RAM doesnotreproduce4CPUdedicatedproductionDB.

Handover code/document checkpoint commits: batchd5a854c, no-skipCI29ee9fa, initialcommittedmaster52d9d36, calibrationprep844af84; later documentation HEAD may differ. K3s separatebranch084717875d5a79356b3dd6e6e5471411814ec94d. Mobilemain ee7546c6997d70494dedd8edd21f22a46803c82f. Inspect gitstatus and CI before executing anything; no implicit approvals from this record.
