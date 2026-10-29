# Temporary isolated calibration lab: approval proposal

Prepared29October2026,Africa/Lagos. Status:PENDING EXPLICIT PAID-RESOURCE APPROVAL. No server, billing configuration, runner registration, firewall or production setting has been changed.

## Existing-resource inventory

GitHub repository3my1234/across-backend-api has0registered self-hosted runners (read-only API). Owner3my1234 is a personal User account. GitHub larger runners require an eligibleTeam/Enterprise organization, so they are not an immediately available option on this owner account. Existing standardActions4-vCPUrunner cannot supply the full independent workload budget. LocalWindows computer has4logicalCPUs/7.9GiBRAM and no usableDocker environment. Existingtwo VPS areproduction and explicitlyexcluded. No suitableconnectedfree/alreadyauthorizedenvironment identified; an unconnected existingisolatedhost can still be considered if supplied.

## Minimum allocation and chosen temporary SKU

Conservative minimum:12independently assigned logicalCPUslots and20GiBavailableRAM beforestartup, typically>=24GiBphysicalRAM. Concurrent existing CPUquotas sum10.5cores, but independent placement andhost/instrumentation reserve need additional slots. Memoryquotas for measuredservices totalabout14.1GiB, beforecontroller/build/cache/OSheadroom. These are labrequirements, not a productionserver purchase recommendation.

ProposedsingleVM:HetznerCCX43,16dedicatedx86vCPUs,64GBRAM,360GBNVMe,Ubuntu24.04LTS,EU-Nuremberg(subjectcheckoutavailability). No backups/snapshots/additionalvolumes/loadbalancer/K3s. OnePrimaryIPv4 for restrictedoperatorSSH; no publicapplication/databaseports. ProviderdefinesoneCCXvCPUasonephysicalCPUthread, not a wholephysicalcore. This proposal relies on dedicated-vCPU terms plus verifiedCPUaffinity/quota evidence, not containerquotas alone.

|Role|CPUset|Existing/selectedquota|RAMlimit|
|---|---|---:|---:|
|PostgreSQL|0-3|4CPU|8GiB|
|Generator|4-5|2CPU|4GiB|
|API1|6|0.75CPU|512MiB|
|API2|7|0.75CPU|512MiB|
|Gateway|8|1CPU|128MiB|
|Notificationworker|9|1CPU|512MiB|
|Redis+mocktransport|10(sharedauxiliaryslot)|0.25CPUeach|128MiBeach|
|Observer/exporter;verifier/seedatdifferentphases|11|0.5CPUobserver|256MiBobserver|
|Host/controller/build/artifacttasks|12-15|reservedhostbudget|remainingRAM|

Criticalroles have disjointCPUsets. Onlyexplicitauxiliaryservices shareoneCPU; aggregatequota0.5CPU. Proposedcgroup-v2workloadparentatlxpres-lab.slice allows0-11; host/controllerprocesses confined12-15 usingseparateparent/affinity configuration on NEWVM only. Do not restrictDocker's parent slice so it cannot delegateworkloadCPUsets. Verify effectiveroot/parent/childCPUsets andquota ancestry. Kernelinterrupts/hypervisor effects still exist and must be measured; no absolutezero-contention or physical-core exclusivity claim.

Overlay:deploy/notification-lab/dedicated-compose.yml. Offline validator:dedicated-allocation.py. Testsacceptedthecorrectmapandrejectedoverlap,4CPUhostandinsufficientRAM; this is NOT liveCPUdedicationverification. Beforeusinganyoverlay,reviewrenderedmergedCompose forunchangedAPI/pool/admission/mocksettings, liveDockerHostConfig.CpusetCpus/NanoCpus/Memory/CgroupParent, cpuset.cpus.effective/cpu.max ancestry,cpustat/throttle/steal,hostmemory,diskandprocessplacement. RejectifproviderCPUsharingclassoractualallocationdiffers. Olderprocess-affinity-onlyrepresentativepreflight mustnotmisclassifycontroller'sintentional4CPUreserve as the whole16CPUhost; validatehost/workloadslice separately.

## Estimated cost and time

OfficialHetznerEUprice-adjustmenttable listsCCX43 atUS$0.5216/hour (EUR0.4423/hour), excludingIPv4/tax. Sixroundedhours=US$3.1296compute; allowaboutUS$3.15beforetax/IProunding. PrimaryIPv4 separatelybilled atpublishedEUR0.50/month; sixhourusageisvery small. FINALcheckoutquote/stock/accounteligibilitymustbeverifiedbeforecreation. Stopifrequireddeposit,setupfee,minimumcharge,regionchangeor6hquoteexceedsapprovedscope; do not assumeaccountactivationorcapacityavailability.

Requestedauthorization:one temporaryVM+onePrimaryIPv4, atmost6hours runtime, totalusagebudgetUS$10includingapplicabletax/IPv4. Expectedwork2-3hours,6hoursallowsboundedsetup/evidence/recoverybuffer. Noautomaticextensions,secondVM,largerSKUorproductionchange. This is a usagebudget, not a guaranteedproviderhardcap:alertsarenotspendinglimits, andshuttingdownaVMdoesnotstopbilling. Confirmexternalexpiry/deletionmonitor beforecreation; deleteVM AND detachedPrimaryIPv4 afterevidenceor6hdeadline, verifybothabsent. Monitorcleanupfailures/retryandnotifyuser; neverleaveastoppedVMincurringcost. Anyaccountprepayment/paymentauthorizationmustbereviewedseparatelyifoutsideapprovedamount.

## Concrete setup and execution plan (afterapproval only)

1.Verifycurrentcheckoutquote,dedicatedCPUclass,region,accounteligibilityandapprovalbudget; recordresourceIDs/creationtime/6hUTCexpiry. Setexternalprovider-APIexpirywatchdog andbillingalert; providercredentialsstayoffthetestrunner. Createonlyapprovedtemporaryresource, withSSH restrictedtooperator andnoinboundapplication/DBports.
2.InstallDockerCompose andrequiredpinnedbuildtools onNEWVM only. Usea single-usecontrolledCIjob,pinnedreviewedbackendsource, noautomaticPRexecution,noproductionsecrets,andmock/syntheticdataonly. Validatehost16CPU/RAM/storageandconfigure/verifydisjointcgroups/CPUsets. NoactiveproductionSSH/DB/Redis/DNSconnections fromlab.
3.Requalifytheoriginal500sessiongenerator for30minutes onallocated2CPU/4GiB, unchangedclienthashes andoriginalheadroom/accountinggates. Keepmocktargetonseparatereservedslot; recordtargetbudgetandactualoverlap. It mustnotborrowDB's4CPUset. Currentgeneratorcodeprovenby40866958782; newenvironmentneedsitsownresourceevidence.
4.Runbounded180secondactual-stackcalibration withDB4CPU/8GiB,gateway1CPU,andunchangedAPI0.75CPU/pool8/admission64,actualworkers,mockedtransports,originalexpandedjourneys/thinktimes/latency/error/authorization/idempotency/reconciliation/recoverygates. Retainexactsource/limits/clienthashes,cgroups/hostCPU/memory/disk,peroperationp50/p95/p99,queuesandSQL. Generatorandsharedhostheadroommustalsoqualify.
5.Onlyafterretainedcalibrationpassesandresource/correctnessreview,executeunchanged30minute500sessionstage,fullwriteinventoryandreal15minreceipt-tail/drain/recovery. Stoponfailure; no1000stage. Preservefailedresultsandunconfirmedcommits. Rejectedsendercandidateisnotapplied; application/migrationsremain29ee9fa candidate.
6.Download/hashsource+rawproof,updatemasterhandover,unregistersingle-useCIrunner,deleteonlyrecordedtemporaryVM/IP,verifydeletionandreportcost. Nochanges tocurrentContabo,Coolify,unrelatedwebsite,PostgreSQL/Redis,K3s,mobileorproduction.

## Official sources (checked29October2026)

-https://www.hetzner.com/cloud/general-purpose/ ?CCX43specifications.
-https://docs.hetzner.com/general/infrastructure-and-availability/price-adjustment/ ?publishedEUhourlyrates.
-https://docs.hetzner.com/cloud/servers/faq/ ?CCXdedicatedCPUdefinition.
-https://docs.hetzner.com/cloud/servers/overview/ ?PrimaryIPpricing.
-https://docs.hetzner.com/cloud/billing/faq/ ?hourlyrounding,shutdown/deletionbilling,alertsarenotlimits.
-https://docs.github.com/en/actions/how-tos/manage-runners/larger-runners/control-access ?larger-runnereligibility.

Nextactionabledecision:approveuptoUS$10foronesixhourtemporarylabwithcreationANDverifieddeletion, orprovidesuitablealreadyownedisolatedresources forread-onlyreview. User'sexplicitinstructionprohibitspaidprovisioningwithoutthatapproval, so no paidactionhasbeentaken.
