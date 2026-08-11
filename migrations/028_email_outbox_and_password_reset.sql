ALTER TABLE users
    ADD COLUMN IF NOT EXISTS password_reset_token_hash TEXT,
    ADD COLUMN IF NOT EXISTS password_reset_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS password_reset_requested_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS session_version INTEGER NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX IF NOT EXISTS users_password_reset_token_hash_idx
    ON users(password_reset_token_hash)
    WHERE password_reset_token_hash IS NOT NULL;

CREATE TABLE IF NOT EXISTS email_suppressions (
    email TEXT PRIMARY KEY,
    reason TEXT NOT NULL CHECK (reason IN ('permanent_bounce', 'complaint', 'manual')),
    source_event_id TEXT,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (email = lower(btrim(email)))
);

CREATE TABLE IF NOT EXISTS email_webhook_events (
    message_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS email_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dedupe_key TEXT NOT NULL UNIQUE,
    recipient_email TEXT NOT NULL,
    recipient_name TEXT NOT NULL DEFAULT '',
    template_type TEXT NOT NULL CHECK (template_type IN ('verification', 'welcome', 'password_reset')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'retry', 'sent', 'suppressed', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_at TIMESTAMPTZ,
    sent_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (recipient_email = lower(btrim(recipient_email)))
);

CREATE INDEX IF NOT EXISTS email_outbox_delivery_idx
    ON email_outbox(next_attempt_at, created_at, id)
    WHERE status IN ('pending', 'retry');

CREATE INDEX IF NOT EXISTS email_outbox_lease_idx
    ON email_outbox(locked_at)
    WHERE status = 'sending';

CREATE INDEX IF NOT EXISTS email_outbox_recipient_idx
    ON email_outbox(recipient_email, created_at DESC);
