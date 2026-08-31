-- Forward-only reconciliation for provider installations that may already have
-- recorded migration 033 before all moderation/audit columns were present.

ALTER TABLE provider_verification_documents
  ADD COLUMN IF NOT EXISTS reviewed_by UUID REFERENCES admins(id) ON DELETE SET NULL;

ALTER TABLE provider_marketplace_events
  ADD COLUMN IF NOT EXISTS actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS actor_admin_id UUID REFERENCES admins(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS event_type TEXT,
  ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE provider_marketplace_events
SET event_type = 'legacy_event'
WHERE event_type IS NULL;

ALTER TABLE provider_marketplace_events
  ALTER COLUMN event_type SET NOT NULL;
