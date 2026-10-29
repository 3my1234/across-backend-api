# Representative CPU calibration preparation ?29October2026

The database host read-only output confirms4visible AMD EPYC vCPUs/KVM and7.8GiB RAM. PostgreSQL container quota is a separate pending read-only check. Gateway-only and DB-only matched measurements support testing4DBCPUs plus1gatewayCPU. No purchase, production resize or reassignment is authorized.

Prepared overlay deploy/notification-lab/representative-compose.yml is NOT used by existing workflows. It retains2generatorCPUs/4GiB, setsDB4CPU/8GiB andgateway1CPU, and inherits unchanged APIs/pools/admission/workers. These are explicit test allocations, not relaxed thresholds or a capacity result.

For a combined-host test, require an existing isolated non-production environment with12availablelogicalCPUs and at least20GiB available memory (typically24GiBtotal). This conservatively covers concurrent declared DB4 +generator2 +APIs1.5 +gateway1 +worker1 +mock/Redis/observer budgets plus host/instrumentation headroom. A split isolated DB/application/generator setup is another option but requires separately reviewed private networking, latency and resource inventory. No larger runner has been configured or purchased. Current standard4CPUCIrunner cannot pass representative_preflight.py; setting a4CPUDBquota there does not independently supply all4cores alongside a fullloadgenerator/APIs/workers.

Before execution:review exact tested application/migration source, generator proof on target environment and actual measured headroom; isolate all email/push/payment transports, retain original client/threshold hashes and full correctness/write/recovery gates, record quota/memory/I/O/hostCPU limits, and obtain any required resource/provisioning approval. Run the bounded180s calibration before sustained500. No automatic1000phase. A production-like CPU/RAM allocation still doesnot establish equivalentCPUmodel/storage/RTT or production capacity.

Until suitable approved isolated resources exist, continue targeted software comparisons on the existing lab; do not borrow the live database or disturb Coolify/unrelatedwebsite. This package has no installation, migration, networking or dispatch actions.
