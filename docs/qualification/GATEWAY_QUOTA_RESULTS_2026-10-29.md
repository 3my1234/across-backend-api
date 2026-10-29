# Gateway-only isolation result ?29October2026

Run40876893888, source8b586a7. Exact existing HAProxy3.0 configuration; two synthetic constant-health backends at0.75CPU, generator2CPU/4GiB, no PostgreSQL/auth/business logic. Six sequential500-request arms; all3000requests/checks succeeded, every k6exit0, no interrupted iterations.

Direct backend p95:25.63/31.42ms. Proxy0.25CPU p95:284.69/257.10ms. Proxy1CPU p95:69.22/69.97ms. ABBA proxy order0.25/1/1/0.25, direct references before/after. This supports gateway quota as an independent latency contributor; it does not prove gateway created503s or that oneCPU fixes application qualification. Retained original API admission counters independently reached330+389=719rejections, including late completed responses afterloadabort;707captured in measured responses.

Artifacts/hashmanifest/analysis:qualification/gateway-probe-20261029/40876893888. This is subsecond fixed-response diagnostics, not sustainedgateway capacity or a production-equivalent500session test. No authentication, authorization, persistence or real notification claims. Docker stats coverage is sparse for such short arms; no precise burstCPU attribution claimed.

Next:review pending full-schema trigger/DBCPU comparison40877066781 at886599f. Earlier40876618230 was cancelled during preparation after detecting profiler parameter-binding semantics; no result used. The corrected harness PREPARE/EXECUTE binds one message UUID per statement shared by allrecipients, identical to controller semantics. Includes prepare/deallocate client overhead in pgbench totals; EXPLAIN isolates query/trigger timings. All rolled-back writes must leave notification/delivery counts unchanged. No application optimization or new calibration yet. Production unchanged.
