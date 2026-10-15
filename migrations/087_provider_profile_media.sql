-- Public business branding only; verification documents remain separate.
ALTER TABLE provider_organizations ADD COLUMN IF NOT EXISTS banner_url text NOT NULL DEFAULT '';
