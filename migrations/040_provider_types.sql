ALTER TABLE provider_organizations
  ADD COLUMN IF NOT EXISTS provider_type TEXT NOT NULL DEFAULT 'mixed',
  ADD COLUMN IF NOT EXISTS provider_type_other TEXT NOT NULL DEFAULT '';

DO $$ BEGIN
  ALTER TABLE provider_organizations
    ADD CONSTRAINT provider_organizations_type_check
    CHECK (provider_type IN (
      'product_merchant',
      'service_professional',
      'property_host',
      'mobility_provider',
      'mixed',
      'other'
    ));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS idx_provider_type_status
  ON provider_organizations(provider_type, verification_status, created_at DESC);
