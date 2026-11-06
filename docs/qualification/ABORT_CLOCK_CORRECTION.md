# Measured-traffic abort clock correction

2026-11-06. No VPS access or load was used for this correction.

The exact k6 v2.3.0 Windows archive was checked against its published SHA256:
112276d495e5741c968e2bc09ea6196099c1275bd6db9ee0875d173c7148ce43.

A no-network reproduction used 16 seconds of setup, local preparation and a
10-second delayed measured scenario. With the existing 15-second abort delay,
the test aborted after only 1.9 seconds of measured traffic. This reproduces
the premature evaluation seen in run 42175549687 without using a server.

`deploy/notification-lab/measured_abort.py` provides a reversible candidate:
every successful sample continues entering the original final p95<500ms and
p99<1500ms gates. An abort-only metric buffers the same samples until 15 seconds
of measured scenario time have passed, then receives all buffered samples and
subsequent samples. No early samples are removed from either evaluation.
HTTP actions, credentials, payloads, resource caps and production watchdogs
are unaffected.

Actual k6 mock executions (20 seconds planned measured traffic):

| Case | Exit | Result |
| --- | --- | --- |
| Original timing, initial 0.6-second transient | 99 | Aborted early |
| Corrected timing, identical transient | 0 | Completed full 20 seconds; final p95 passed |
| Corrected timing, continuously 600ms samples | 99 | Aborted at approximately 15.96 measured seconds |

The actual candidate buffering code also passed Node execution checks that
600ms and 700ms early samples remain in the abort metric after the grace period.
Reversing the candidate restores the exact original client source.

Reporting now distinguishes acknowledged-write integrity, full offered-write
inventory, generator resource-headroom failures and incomplete accounting.
These diagnostics do not change qualification acceptance.

Focused checks: 3 outcome tests, 19 existing trial/generator/cleanup tests,
2 timing-candidate tests and 3 retained-proof integrity tests passed.

Evidence lives in the shared workspace under qualification/k6-early-transient.log,
k6-corrected-transient.log, k6-corrected-slow.log and their .exit files. The
reproduction is qualification/reproduce-k6-abort-clock.js; it sends no HTTP.

Independent qualification subsequently PASSED in GitHub run42178904513 at
f6efa17cf4bc69cea1ddf0df65e66728a9c5c110. All 500 authenticated sessions were
sustained for 1800 seconds (1804.60 measured seconds), with 449841 requests
offered and completed, no dropped iterations, all original thresholds passing
and successful disposable cleanup. CPU p95 was19.23% of the two-core quota,
burst p9543.64%, throttling0.0166%, peak memory2771558400 bytes against4GiB,
and OOM events zero. This is a synthetic-target generator result, not an
application/database capacity result.

The trial runner and disposable rehearsal now generate the exact independently
verified client hash0beb48eadb26144c2cbfea5820bf5f01c179aa3f6d6ff21f00c05d8da7c36290.
Authentic CI proof members are retained under
deploy/cohost-lab/proofs/generator-42178904513 and anchored by their gate/client
checksums. Historical proof remains valid for its own client, and cannot be
mixed with the corrected client. No historical artifact download is needed
at execution time. A successful exact-source package rehearsal is still
required before another VPS attempt.
