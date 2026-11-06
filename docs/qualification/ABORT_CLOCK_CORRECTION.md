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

The corrected candidate is deliberately not enabled in trial_runner.py:
the runner still enforces the old independently qualified executed-client hash.
Do not change that hash to pretend the modified generator has already passed
its required independent 500-session/30-minute qualification. That independent
validation and a new exact-source package rehearsal remain prerequisites to
another VPS attempt. Existing historical proof remains unchanged and valid
for its own client. No 500-session application capacity claim follows from
these offline timing results.
