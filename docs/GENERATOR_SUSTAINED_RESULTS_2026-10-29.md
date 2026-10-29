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
