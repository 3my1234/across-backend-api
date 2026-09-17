ALTER TABLE provider_marketplace_events
    ADD COLUMN IF NOT EXISTS read_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_provider_marketplace_events_provider_created
    ON provider_marketplace_events (provider_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_provider_marketplace_events_provider_unread
    ON provider_marketplace_events (provider_id, created_at DESC, id DESC)
    WHERE read_at IS NULL;

ALTER TABLE email_outbox
    DROP CONSTRAINT IF EXISTS email_outbox_template_type_check;

ALTER TABLE email_outbox
    ADD CONSTRAINT email_outbox_template_type_check
    CHECK (template_type IN (
        'verification',
        'welcome',
        'password_reset',
        'marketplace_activity'
    ));
