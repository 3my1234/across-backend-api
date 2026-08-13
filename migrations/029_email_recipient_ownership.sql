ALTER TABLE email_outbox
    ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE CASCADE;

UPDATE email_outbox o
SET user_id = u.id
FROM users u
WHERE o.user_id IS NULL
  AND lower(btrim(o.recipient_email)) = lower(btrim(u.email))
  AND (
    o.dedupe_key = 'welcome:' || u.id::text
    OR o.dedupe_key LIKE 'verification:' || u.id::text || ':%'
    OR o.dedupe_key LIKE 'password-reset:' || u.id::text || ':%'
  );

UPDATE email_outbox
SET status = 'failed', locked_at = NULL,
    last_error = 'legacy email has no verified account owner', updated_at = now()
WHERE user_id IS NULL
  AND status IN ('pending', 'retry', 'sending');

CREATE INDEX IF NOT EXISTS email_outbox_user_created_idx
    ON email_outbox(user_id, created_at DESC);
