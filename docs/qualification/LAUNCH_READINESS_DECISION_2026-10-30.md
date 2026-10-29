# ATL Xpress launch decision ? 2026-10-30

Status: no broad-launch capacity approval. No production change or live load authorized.

## Items requiring evidence before a release decision
- Demonstrate backup restoration in an isolated destination; archive readability alone is insufficient.
- Verify production alert receipt, rollback/recovery procedure and deployed security configuration.
- Complete device acceptance for authentication, image upload and critical customer/provider journeys; distinguish already accepted APK UI from unverified devices/flows.
- Verify payments/subscription correctness against the intended live configuration without real charges unless separately authorized.
These are readiness evidence gaps, not newly demonstrated application defects.

## Capacity constraints
- Earlier isolated 100-session, 30-minute HTTP workload passed. This does not define a production safe-user limit.
- Generator alone passed 500 sessions for 30 minutes; application 500-session qualification remains unpassed.
- Shared-server setup stopped before staging on the latest database-vantage observer wall-time check. Healthy HTTP transfer took 1.278215s, observer took 2.245950s. Historical overhead cannot be localized retrospectively.
- Any controlled rollout requires separately reviewed operational monitoring, onboarding controls, stop/rollback authority and production readiness evidence; do not advertise a numeric production capacity from these tests.

## Deferred
- K3s installation/migration, new VPS purchases, Redis GEO redesign and 1,000-session testing.
- Mobile/UI remains separate from backend qualification.

## Immediate execution
Process-launch and communicate/wait durations are now separate diagnostics; curl's two-second transfer cap, existing two-second observer gate, TLS checks and no-retry behavior remain unchanged. Timeout kills and reaps the child. 45 safety tests passed locally. Refresh current staged tools using DEFAULT Resume-CohostSetup.ps1, reusing server image archives. Both genuine baselines must pass before setup/identity connectivity can continue. No load generation. If blocked again, use the actual recorded process phase; do not repeatedly rerun or weaken gates. SSH requires operator-local password entry; agent cannot execute the host steps unattended.
