-- Existing transport profiles are deliberately unclassified; never infer a
-- motorcycle/car role from a business name or change an existing agreement.
ALTER TABLE provider_organizations ADD COLUMN IF NOT EXISTS transport_role text NOT NULL DEFAULT ''
 CHECK (transport_role IN ('','driver','rider'));
