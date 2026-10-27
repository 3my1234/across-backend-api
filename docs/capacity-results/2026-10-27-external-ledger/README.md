# Isolated worker evidence

These are analyses of synthetic-only experiments, not production capacity claims.

- Comparison source: edb5b92d3b33686cf61bd4a133e5a6f9d89dd12a, run 40587689346.
- Fault source: 66d9567602ad21dd9f0b4aaa483b59310aa8a205, run 40591221683.
- Complete raw evidence is retained locally under qualification/external-ledger-workers-20261027/ and in GitHub Actions artifacts (14-day retention).
- Download the raw artifacts into this directory as 40587689346/ and 40591221683/ respectively, then run the two Python analyzers. They check original gates and reconcile the retained journals before producing JSON results. Both use only the standard library and perform no network/production operations.

Example:

```powershell
gh run download 40587689346 --repo 3my1234/across-backend-api --name external-ledger-workers-40587689346 --dir 40587689346
gh run download 40591221683 --repo 3my1234/across-backend-api --name external-ledger-workers-40591221683 --dir 40591221683
python analyze_comparison.py
python analyze_faults.py
```

The comparison dump itself remained inside the disposable CI workspace; its hash is retained to prove matched inputs within that run. Fresh seeding reproduces scenario/cardinalities with new synthetic UUIDs/timestamps. Exact recipient delivery, production capacity and physical-device behavior are not qualified.
