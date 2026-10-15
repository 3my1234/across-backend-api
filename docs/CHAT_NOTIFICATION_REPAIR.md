# Provider chat transaction repair

The production API returned HTTP 500 for a customer message at 14:20:56 on
2026-10-15. PostgreSQL logged SQLSTATE 42P10 in
enqueue_provider_native_notification: its ON CONFLICT(event_key) target did not
match notifications_event_key_unique, which is a partial index with WHERE
event_key IS NOT NULL. Because provider events and messages commit together,
the trigger error rolled back the message. Repeated client retries could not
repair that server error.

Migration 090_provider_notification_conflict_target.sql replaces the trigger
function with the matching conflict predicate. It preserves notification data,
recipients, privacy-safe push text, deduplication and the existing indexes.
Existing migration 085 is left intact. The legacy request-status notification
insert in Go now uses the same predicate. The separately logged profile scan
failure is fixed by treating missing optional name/avatar fields as empty.

The earlier community fixture incorrectly used event_key TEXT UNIQUE. It now
uses the production partial index. The uncorrected 085 trigger was reproduced
failing with exactly 42P10 before the repair. Tests now cover duplicate event
replays, service send/retry/provider reply through the unified chat controller,
and product/service messages with actual native/web notification triggers and
outboxes installed. CI includes that product/service transaction test.

Deploy the API from latest main with RUN_MIGRATIONS=true (already authorized).
Leave the worker flag false. Confirm migration 090 is recorded on the production
database; /app/across-migrate can safely be rerun in the newly deployed API.
Then check /api/v1/ready and retry the retained draft from the existing APK.
No new APK, worker deployment or provider UI deployment is required to repair
the message transaction. If migration 090 has not been applied, the old trigger
continues to fail regardless of client version.

Accept by sending customer -> service provider, provider -> customer, and a new
product conversation, then confirming the message is present once after retry
and the provider notification arrives. Check for no new 42P10 in PostgreSQL.
No production deployment or acceptance is claimed by local tests.

The optional mobile follow-up preserves server message/error strings, HTTP
status, request ID and rate-limit wait duration instead of masking errors as
"Could not load community." It retains the existing draft and idempotency
reference and never automatically repeats message mutations.
