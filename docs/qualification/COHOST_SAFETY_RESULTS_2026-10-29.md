# Cohost preparation safety validation

29 October 2026. Free GitHub Actions run 40922287424 PASSED at source c50fc3d5de7d27c759018ece64ba3f25b7c2996e.

- Ten safety policy/CPU arithmetic checks passed on Linux.
- Split-host renderer checks passed: pinned digests, run labels, loopback ports, internal network, unchanged pool/admission settings and generator exclusion.
- An actual disposable CI test container was stopped by full ID with matching nonce label.
- An unrelated CI sentinel with a different ownership label was refused BEFORE mutation and remained running after the owned container stopped. The expected ownership-rejection traceback is a successful negative test, not a qualification failure.
- Logs/metadata and SHA256SUMS retained under qualification/cohost-safety-20261029/40922287424/.

This proves the tested policy and owned-container stop behavior only. It does not validate production-host cgroup enforcement, the full watchdog/peer lease chain, SSH forwarding, split-host fixture reset/reconciliation adapters or 500-session capacity. Read deploy/cohost-lab/README.md outstanding gates before live setup approval. No VPS access or changes occurred; no paid resources, K3s, merge or release.
