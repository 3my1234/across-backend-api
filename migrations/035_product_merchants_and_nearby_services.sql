ALTER TABLE provider_organizations
  ADD COLUMN IF NOT EXISTS capabilities TEXT[] NOT NULL DEFAULT ARRAY['services']::text[];

ALTER TABLE provider_listings
  ADD COLUMN IF NOT EXISTS service_radius_km NUMERIC(8,2),
  ADD COLUMN IF NOT EXISTS is_mobile_service BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS is_available_now BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_provider_listings_nearby
  ON provider_listings(listing_type, status, latitude, longitude, id)
  WHERE latitude IS NOT NULL AND longitude IS NOT NULL;

ALTER TABLE products
  ADD COLUMN IF NOT EXISTS provider_id UUID REFERENCES provider_organizations(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS fulfillment_mode TEXT NOT NULL DEFAULT 'atlantic_import',
  ADD COLUMN IF NOT EXISTS moderation_status TEXT NOT NULL DEFAULT 'approved',
  ADD COLUMN IF NOT EXISTS moderation_notes TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS moderated_by UUID REFERENCES admins(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS moderated_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS catalog_version BIGINT NOT NULL DEFAULT 1;

DO $$ BEGIN
  ALTER TABLE products ADD CONSTRAINT products_fulfillment_mode_check
    CHECK (fulfillment_mode IN ('atlantic_import','merchant_local'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
  ALTER TABLE products ADD CONSTRAINT products_moderation_status_check
    CHECK (moderation_status IN ('draft','pending','approved','rejected','suspended','archived'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

UPDATE products SET published_at=COALESCE(published_at,created_at), moderated_at=COALESCE(moderated_at,created_at)
WHERE provider_id IS NULL AND moderation_status='approved';

CREATE INDEX IF NOT EXISTS idx_products_public_catalog
  ON products(moderation_status, is_active, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_products_provider_catalog
  ON products(provider_id, created_at DESC, id DESC);

ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS provider_id UUID REFERENCES provider_organizations(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS fulfillment_mode TEXT NOT NULL DEFAULT 'atlantic_import';
DO $$ BEGIN
  ALTER TABLE orders ADD CONSTRAINT orders_fulfillment_mode_check
    CHECK (fulfillment_mode IN ('atlantic_import','merchant_local'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

ALTER TABLE order_items
  ADD COLUMN IF NOT EXISTS provider_id UUID REFERENCES provider_organizations(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS fulfillment_mode TEXT NOT NULL DEFAULT 'atlantic_import';

CREATE TABLE IF NOT EXISTS merchant_ledger (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id UUID NOT NULL REFERENCES provider_organizations(id) ON DELETE RESTRICT,
  order_id UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
  event_key TEXT NOT NULL UNIQUE,
  currency_code CHAR(3) NOT NULL,
  gross_amount NUMERIC(14,2) NOT NULL CHECK (gross_amount >= 0),
  platform_fee NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (platform_fee >= 0),
  net_amount NUMERIC(14,2) NOT NULL CHECK (net_amount >= 0),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','available','paid','reversed')),
  available_at TIMESTAMPTZ,
  paid_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_merchant_ledger_provider
  ON merchant_ledger(provider_id, status, created_at DESC, id DESC);
