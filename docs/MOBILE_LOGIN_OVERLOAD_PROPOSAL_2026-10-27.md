# Proposed mobile handling of temporary login overload

Status: **review proposal and isolated transport prototype; not integrated, merged, built or released**. Branch `experiment/login-overload-proposal`, based on mobile `3fd8df706d79e5b479266423cb6d155299cc2edb`. No application entry point imports the prototype. Existing sign-in behavior is unchanged.

## Problem and evidence

The isolated authentication investigation accepted 5/100 initial login requests; the other 95 received controlled HTTP 503 responses. A harness-only Retry-After-aware model eventually authenticated all 100, with a maximum observed time of 11.29 seconds. That experiment used synthetic traffic on a private Docker network, not a phone/network test. The released mobile `App.tsx` currently makes one password-login request and presents an error if it fails.

The current backend routes password login through a two-slot admission guard per API, returning `Retry-After: 2` and `Cache-Control: no-store` before the login controller runs. This protects password-processing CPU. The client proposal does not alter that limit, password hashing, IP/account limits or server authorization.

## Proposed behavior

1. Only an explicit user submission to the configured API's exact `POST /api/v1/auth/login` endpoint can start this policy. Snapshot credentials and country headers once in memory; every retry uses the same submission. Never log or persist the password, request body or reply token in retry telemetry.
2. Automatically retry only a completed HTTP **503** response with a valid **Retry-After** header. Accept integer delay-seconds, or a valid current HTTP-date only when the same response also includes a valid Date header. For date-based waits, use the difference between server headers plus one second for precision; never use the phone clock to shorten the wait. Without a valid Date header, stop automatic date-based retries. Missing/malformed headers, network failures, connection loss, timeouts, 401/403/409/422, 429, and other 5xx statuses stop automatic retries. In particular, an unknown outcome is never interpreted as permission to send again.
3. Keep at most one request in flight. Wait at least Retry-After, with a minimum one-second floor for a zero/past date, and add independently generated uniform jitter of 0-1,000 ms. This spreads retries rather than aligning them on a two-second boundary. It cannot guarantee the absence of aggregate retry load during a large campaign; backend admission/rate protection remains essential.
4. Bound the entire transport sequence, including response-body reads, to **six total attempts and 30 seconds**, whichever stops first. Use a monotonic clock for elapsed time. Do not shorten a server wait to fit the budget: if it cannot fit, stop with a friendly manual-retry message. These are proposed client budgets, not proven mobile-network capacity targets.
5. The UI should say `Signing in...`, then `Sign-in is busy. Trying again in about 3 seconds (2 of 6). You can cancel.` Display the status accessibly and disable email/password edits and all competing authentication submissions during the attempt. Show a separate, usable **Cancel sign-in** control. The text is an approximate wait notice, not a continuously updated countdown.
6. On Cancel, Back, account/mode changes, component teardown or app backgrounding, abort the wait/in-flight request and invalidate the attempt generation. Do not silently restart on foregrounding. Restore editable controls; a new attempt requires an explicit user action. Background-location sharing is unrelated and must remain unchanged.
7. A late HTTP response must never open a session for an obsolete attempt. On success, validate the same response contract as current login, then serialize session persistence with existing OAuth/login/logout writes. Recheck the attempt generation before and after asynchronous persistence. Do not clear or overwrite a newer account's session while cleaning up an old attempt.
8. On exhaustion show `Sign-in is temporarily busy. Please wait a moment, then try again.` Retain form values in component memory so the user need not retype; never save the password to storage. Preserve existing invalid-credentials and unverified-email behavior without revealing whether another account exists.

Registration, verification-email requests, reset-password submissions, OAuth, admin login, purchases and token refresh are explicitly outside this policy. **No automatic registration retry is proposed.** Registration needs a separate reviewed server idempotency design before any replay is considered.

## Prototype and checks

`components/loginOverload.ts` is a dependency-free transport prototype. It enforces the response/header policy, six-attempt budget, monotonic 30-second deadline, Retry-After floor plus jitter, cancellation and suppression of late transport results. The injected clock/transport make its boundaries deterministic to test. The caller must include the entire response-body read in the transport promise.

`node scripts/test-login-overload.cjs` checks immediate success, recovery, all-attempt exhaustion, exact wait/jitter bounds, HTTP-date parsing, malformed/missing headers, non-retryable statuses, network ambiguity, deadline, cancellation before/in a request or wait, late success and timer cleanup. A focused strict TypeScript check passes. The existing session-startup regression also passes; the proposal does not change local session restoration.

This prototype **does not yet implement or prove the screen's submission lock, UI, lifecycle binding, asynchronous SecureStore/session-commit serialization, or physical-device behavior**. Those are required integration acceptance items, not features claimed by these transport tests. No whole-app/native build has been run.

## Review and integration acceptance

After approval of the proposed policy, integrate it in a separate mobile change with a synchronous ref-based submission lock (React's `busy` state alone is insufficient). Hold the lock through cancellation settlement and session persistence. Test rapid double-taps, competing signup/Google actions, editable credentials, Back/cancel during request/wait/persistence, background/foreground, unmount, and a second account starting while an old response completes. Require exactly one accepted session and no stale token persistence.

Test against the existing isolated login-burst harness using this actual client policy; do not substitute the earlier deterministic harness retry result. Confirm attempt budgets, completion/rejection distribution, Retry-After compliance and no uncontrolled retry waves. Then test on a physical Redmi and another supported device with slow/offline/interrupted networks and real keyboard/navigation behavior. Verify the implemented server-header-based HTTP-date behavior with skewed device clocks during integration; the current backend uses integer delay-seconds.

Any production build, merge, deployment, infrastructure or server-limit change requires further approval. This proposal authorizes none of those actions and is not a recommendation to advance to 500 sessions.
