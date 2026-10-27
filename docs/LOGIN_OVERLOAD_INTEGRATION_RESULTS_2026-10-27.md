# Experimental mobile login integration, 27 October 2026

Exact local tested source: `8f40420719c8a052650e20f0b52a036f282c6f1b`. Complete source patch and test evidence: `qualification/mobile-login-integration-20261027/`.

Status: implemented and tested on `experiment/login-overload-proposal`; **not merged, deployed, built into an APK or qualified on a physical device**. Based on mobile `3fd8df706d79e5b479266423cb6d155299cc2edb` and the earlier transport prototype `8d4f1626c74188a5fb6b729700b4f3d11751dba2`.

The actual `App.tsx` login handler now uses the previously reviewed transport: only exact password-login submissions can retry a completed 503 with valid Retry-After; six total attempts or 30 seconds, server wait plus 0-1 second jitter. Date-based waits require the same response's valid Date header. Network errors, timeout ambiguity, 429, other statuses and registration are not automatically retried. Signup retains its single submission, referral validation, provider/customer context and email-verification gate.

`authAttempts.ts` provides synchronous exclusion across password login, signup, Google sign-in, verification resend and password-reset requests. This closes the interval before React renders disabled controls. The screen disables competing actions and credential edits, displays accessible busy/retry feedback, and offers Cancel sign-in. Back can cancel password login; backgrounding or unmount invalidates it. Foregrounding does not silently resume it. OAuth browser transitions are preserved rather than treated as password-login background cancellation.

Session writes, deletion and local restoration share a serialized queue across component remounts in the same JS runtime. An old expiry is invalidated before replacing its token. Current-attempt checks surround every asynchronous persistence stage; cancelled writes clean up before a newer writer can commit. Logout invalidates queued writes immediately. A stale request/finally block cannot release another attempt's lock, open an obsolete session or erase a newer stored account. Invalid/expired response envelopes are rejected. No password is persisted or added to logs.

Checks passed:

- Transport cases: Retry-After/date validation, floor/jitter bounds, attempt/deadline limits, network/status exclusions, cancellation, late results and timer cleanup.
- Coordination/storage cases: rapid admission, stale-owner cleanup, cancellation during initial-expiry/token/final-expiry writes, logout/new-account ordering, remount/restoration ordering, storage failures and invalid sessions.
- Execution of actual App handlers and AuthScreen function against synthetic ports: rapid taps, competing signup/Google, stable credential snapshot across retries, signup and 401 single-shot behavior, interrupted requests, late success, cancellation during storage, background/unmount, busy accessibility and usable Cancel/Back.
- Existing session-startup, auth/workspace, provider-entry, workspace-transport, referral and keyboard regressions; full TypeScript checking. ESLint has no errors; baseline warnings are compared in the retained evidence.

These are deterministic JS/component-function tests, not a native renderer or real mobile-network measurement. ADB reported **no connected devices**. Redmi touch targets, keyboard layout, OS lifecycle timing, real SecureStore behavior and performance on an actual slow/interrupted network remain unqualified. Existing system-storage failures can still prevent storage operations; tests ensure failures do not deliberately open a session or leave a new token paired with an old valid expiry.

The old auth/workspace test fixture was adapted to supply the new shared lock and to reserve distinct OAuth attempts for independent journeys. Its original verification, provider context, duplicate callback, signed-identity and failure assertions remain.

Release recommendation: ready for code review and physical-device/development-client acceptance; **not approved for release**. Keep backend password hashing, admission/rate limits and registration semantics unchanged. No 500-session test, EAS action, native package installation, production mutation or infrastructure purchase occurred.

Reproduce with `npm run test:auth-overload`, `node scripts/test-auth-workspace.cjs`, `npm run typecheck`, and ESLint on App/Screens/authAttempts/loginOverload. All source changes remain isolated on the experimental mobile branch.
