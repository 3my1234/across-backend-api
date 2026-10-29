# Sender fast-path comparison: rejected ?29October2026

Measured run40881068109 at8367871 PASSED execution/equality/rollback gates. Earlier40879057063 was preparation-only despite GitHubgreen and supplies no benchmark result; corrected routing now fails closed on missing measurement artifacts.

|Arm|Function|TPS|Mean transaction ms|Query plan elapsed ms|Sender trigger ms|
|---|---|---:|---:|---:|---:|
|1|Original|235.262|34.005|15.292|5.679|
|2|Candidate|245.534|32.582|15.233|5.521|
|3|Candidate|245.170|32.630|16.322|5.929|
|4|Original|245.273|32.617|15.040|5.426|

The candidate is indistinguishable from the final original throughput, and trigger elapsed time did not consistently improve. The small average difference is within the original-arm variation. REJECTED:do not adopt it, create a migration for it or claim a performance win. No application/runtime/migration source change.

All18016helper result comparisons matched (4504eacharm),19417rollback transactions,zerofailed transactions, actualnative/webtrigger invocations and before/after delivery counts passed. Fixtures includeordinary/provider/foreign membership, missing flags/IDs and invalidUUID cases; they do not prove every browser delivery or physical-device scenario. Eightclients,2DBCPU,8GiB,20seconds/arm, ABBA. Prepare/deallocate/network/rollback overhead included; no p95transactiondistribution collected here. Do not compare TPS across different GitHubhosts as a controlledbefore/after; onlywithin-run pairs qualify.

Evidence:qualification/sender-profile-20261029/40881068109/artifacts(60files),analysis.json,SHA256SUMS.json. Candidate remains isolated under deploy/notification-lab for reproduction, explicitly rejected. Originalfunction restored by harness before disposablelabcleanup.

## Next justified action

Representative CPU calibration, NOT another SQL rewrite or unchanged500rerun. Independent measured evidence:gateway0.25CPU adds~257?285ms p95versus69?70ms at1CPU; DB2CPU yields360?422rollbackTPS versus640?645at4CPU. These are targeted findings, not whole-platform orproductioncapacity. Productionhost4visiblevCPUs/7.8GiB is verified; effectiveDockerPGquotacheckis pending useroutput.

Prepared representative overlay/preflight requires existing approvedisolatedresources with room forDB4CPU/8GiB,generator2CPU/4GiB,gateway1CPU plus unchangedAPIs/workers/Redis/observers. Combinedhost conservativecheck12CPU/20GiBavailable; splitisolatedhostspossibleafterprivate-network/resource review. Currentstandard4CPUrunner cannot independentlyprovide these allocations. No larger runner, server purchase, existinghost reassignment or productionchange authorized/performed. Package is unused and failclosed. SeeREPRESENTATIVE_CPU_CALIBRATION_2026-10-29.md.

Full500/1000 remain blocked; no newcalibration run was dispatched after failed40872220566. Its707HTTP503/abort remainsfailed, while generator40866958782 remainspassed. No pool/admission/password/hash/latency/error/integrity threshold weakened. K3s preparation and acceptedmobile remain separate.
