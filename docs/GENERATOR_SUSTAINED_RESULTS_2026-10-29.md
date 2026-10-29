# Independent generator sustained qualification ? 29 October 2026

**Result: FAILED the generator memory-headroom gate. The 8 GiB database calibration and full application500 stage remain blocked.**

Run https://github.com/3my1234/across-backend-api/actions/runs/40855200111, measured source `ac854e5b597a564c96ed2bbddda7d14315261d74`, branch `experiment/generator-headroom`. It completed the full measured workload; this is not an early-abort or application-capacity failure.

| Measurement | Actual result |
|---|---:|
| Measured duration | 1804.592 seconds |
| Offered/completed requests | 449,839 / 449,839 |
| Measured requests/second | 249.27 |
| Distinct active identities | 500 |
| Snapshots with500VUs | 1800 |
| Original workload thresholds | All passed |
| Generator CPU p95, fraction of2CPU quota | 17.43% |
| Initial6s CPU p95, fraction of quota | 31.45% |
| Throttled CPU periods | 0.0166% |
| Peak total cgroup memory | 2,147,627,008 bytes (~2GiB) |
| Declared memory ceiling | 2,147,483,648 bytes (2GiB) |
| Required memory headroom | Peak below70%of2GiB; FAILED |
| OOM events/kills | 0 / false |
| k6 exit code | 0 |

Every assertion except `generator_memory_under_70pct` passed, including duration, CPU coverage, no dropped iterations, preparation/cache use, all500 identities and offered/completed/mock-received accounting. Do not relabel the failed overall gate as qualified merely because HTTP checks passed.

## Memory evidence and uncertainty

The artifact includes a1,663,594,076-byte time-series JSON file written from the generator container to the mounted exchange. The mock processed3,772,554,011response bytes. Total cgroup memory includes anonymous process allocations and charged filesystem cache. The original sampler did not retain memory.stat, so this run cannot distinguish process growth from output-file cache. Output-cache pressure is a specific hypothesis, not a proven root cause. No password/security/app settings changed.

Full artifact retained under `qualification/generator-headroom-20261029/40855200111/artifacts/`. A small range-verified gate/source extraction is also under `quick-proof/`; the full download completed. Independent reconstruction from the full raw series matches every recorded gate field exactly. A SHA256 manifest covering30 artifact files is saved beside it; original evidence was not overwritten.

## Smallest next investigation

A focused180-second diagnostic was dispatched as run https://github.com/3my1234/across-backend-api/actions/runs/40864170840 at source `5dce8fe`, same branch. It keeps the exact measured client hashes,500identities, two CPUs,2GiB ceiling, actions and original thresholds. It adds only anonymous/file/cache memory counters and omits repeating the already-known baseline arm. This diagnostic is not a replacement for a passing1,800-second gate; calibration explicitly requires the latter.

Do not increase memory, change the70%resource threshold, discard measured responses or advance the application workload merely to manufacture a pass. First classify memory. Depending on evidence, propose or implement the smallest authorized instrumentation correction or properly sized isolated generator allocation, preserving unchanged client/workload and all resource headroom thresholds. No purchase, production operation or K3s action is authorized by this result.

## Memory diagnostic result and bounded recording correction

Same-client/same2CPU/2GiB stage40864170840 at5dce8fe PASSED183.164seconds,46,686offers=completions. At its total-memory peak1,034,498,048bytes, memory.stat showed848,855,040anonymous bytes and175,210,496file-cache bytes. Generated JSON length175,237,304bytes closely matches the charged file cache. This directly establishes instrumentation-file cache overhead in the short run; exact long-run anonymous/file split remains unrecorded, so do not claim that all long memory growth was proven file cache.

Lossless recording correction b6e475b remains inside the SAME generator container/cgroup: k6 emits every JSON metric through a bounded FIFO to gzip-1; compression CPU/memory/cache remain charged to the unchanged2CPU/2GiB budget. After the measured container exits, the host validates gzip and reconstructs the complete original JSON for the unchanged analyzer. No metric sampling, discarded response bodies, changed workload, higher budget or relaxed70%headroom gate. Original CPU and latency limits also remain.

CI includes isolated recorder contract checks for byte-exact retention, original k6 error status and compression failure rejection. Validation run40865534032 atb6e475b is RUNNING as a focused180-second stage. Only after it passes may the unchanged1,800-second generator be rerun. Database calibration and actual500 remain blocked until a full sustained gate passes.

## Recorder validation rejected; explicit generator sizing

Run40865534032 atb6e475b completed183.179s and46,686offers=completions with all original workload checks passed. Lossless-recorder contracts passed. Peak generator memory fell to870,961,152bytes (~831MiB), but first6s CPU p95 reached88.93%of2CPU, exceeding the unchanged85%gate. Steady CPU p9535.41%, throttled periods0.4369%, noOOM. This experiment FAILED and the recorder is rejected; no full compressed-recorder sustained run was dispatched.

The next controlled variable is generator memory allocation, not workload or threshold: restore the original direct recorder, retain2CPU and70%memory-headroom/85%burstCPU/70%steadyCPU gates, explicitly allocate4GiB on the existing standard runner. The known long output1.66GB plus short observedanonymous~849MB motivates this sizing. No new host, paid runner or production resource. The gate verifies the declared allocation equals actual DockerMemory so a larger denominator cannot silently hide a smaller container. Unknown/undeclared allocations are rejected; six local Python tests and exact client hashes passed.

New1,800s run https://github.com/3my1234/across-backend-api/actions/runs/40866958782 at `5dc1fc983b5e157d82ea86a6c546c0b2cb4f592c` is RUNNING with direct recording,2CPU/4GiB. This is a changed isolated allocation and must be reported explicitly; it is not a2GiB headroom pass. Application/database traffic remains unstarted.

The actual-stack calibration reverted compression at3498722 and now binds generator_memory_limit_bytes to its explicit requested generator allocation at4d658ef; default4g matches this candidate. For DB8g/gen4g it requires14GiB available host memory before startup. If the existing runner cannot meet that, stop as an environment prerequisite failure, not an app failure. Full500 and1,000 remain gated.
