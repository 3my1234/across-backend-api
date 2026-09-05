-- Route-aware marketplace fulfilment. Atlantic-owned imports continue to use
-- order_batches; merchant routes use isolated order fulfilments and manifests.

ALTER TABLE products DROP CONSTRAINT IF EXISTS products_fulfillment_mode_check;
ALTER TABLE products ADD CONSTRAINT products_fulfillment_mode_check
  CHECK (fulfillment_mode IN ('atlantic_import','merchant_local','merchant_cross_border'));

ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_fulfillment_mode_check;
ALTER TABLE orders ADD CONSTRAINT orders_fulfillment_mode_check
  CHECK (fulfillment_mode IN ('atlantic_import','merchant_local','merchant_cross_border'));

ALTER TABLE products
  ADD COLUMN IF NOT EXISTS inventory_country_code TEXT NOT NULL DEFAULT 'NG',
  ADD COLUMN IF NOT EXISTS inventory_city TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS inventory_location TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS stock_state TEXT NOT NULL DEFAULT 'locally_available',
  ADD COLUMN IF NOT EXISTS handling_time_hours INTEGER NOT NULL DEFAULT 24,
  ADD COLUMN IF NOT EXISTS delivery_min_days INTEGER NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS delivery_max_days INTEGER NOT NULL DEFAULT 7,
  ADD COLUMN IF NOT EXISTS delivery_methods TEXT[] NOT NULL DEFAULT ARRAY['delivery'],
  ADD COLUMN IF NOT EXISTS return_policy TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS atlantic_last_mile BOOLEAN NOT NULL DEFAULT FALSE;

DO $$ BEGIN
  ALTER TABLE products ADD CONSTRAINT products_stock_state_check
    CHECK (stock_state IN ('locally_available','foreign_stock','import_on_demand'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
  ALTER TABLE products ADD CONSTRAINT products_delivery_window_check
    CHECK (handling_time_hours >= 0 AND delivery_min_days >= 0 AND delivery_max_days >= delivery_min_days);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS order_fulfillments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id UUID NOT NULL UNIQUE REFERENCES orders(id) ON DELETE CASCADE,
  provider_id UUID REFERENCES provider_organizations(id) ON DELETE RESTRICT,
  route TEXT NOT NULL CHECK (route IN ('atlantic_import','merchant_local','merchant_cross_border')),
  owner TEXT NOT NULL CHECK (owner IN ('atlantic','merchant','atlantic_last_mile')),
  status TEXT NOT NULL DEFAULT 'pending',
  origin_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
  delivery_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
  carrier TEXT NOT NULL DEFAULT '',
  tracking_number TEXT NOT NULL DEFAULT '',
  tracking_url TEXT NOT NULL DEFAULT '',
  current_location TEXT NOT NULL DEFAULT '',
  estimated_delivery_at TIMESTAMPTZ,
  accepted_at TIMESTAMPTZ,
  dispatched_at TIMESTAMPTZ,
  handed_to_atlantic_at TIMESTAMPTZ,
  delivered_at TIMESTAMPTZ,
  version BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS fulfillment_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  fulfillment_id UUID NOT NULL REFERENCES order_fulfillments(id) ON DELETE CASCADE,
  actor_type TEXT NOT NULL CHECK (actor_type IN ('system','merchant','admin','buyer')),
  actor_id UUID,
  previous_status TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  notes TEXT NOT NULL DEFAULT '',
  location TEXT NOT NULL DEFAULT '',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  idempotency_key TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(fulfillment_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS merchant_manifests (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id UUID NOT NULL REFERENCES provider_organizations(id) ON DELETE RESTRICT,
  manifest_code TEXT NOT NULL UNIQUE,
  origin_country_code TEXT NOT NULL,
  origin_city TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed','dispatched','completed','cancelled')),
  cutoff_at TIMESTAMPTZ NOT NULL,
  closed_at TIMESTAMPTZ,
  dispatched_at TIMESTAMPTZ,
  version BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS merchant_manifest_orders (
  manifest_id UUID NOT NULL REFERENCES merchant_manifests(id) ON DELETE CASCADE,
  order_id UUID NOT NULL UNIQUE REFERENCES orders(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (manifest_id, order_id)
);

CREATE TABLE IF NOT EXISTS merchant_manifest_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  manifest_id UUID NOT NULL REFERENCES merchant_manifests(id) ON DELETE CASCADE,
  actor_id UUID NOT NULL,
  previous_status TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  notes TEXT NOT NULL DEFAULT '',
  idempotency_key TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(manifest_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_products_route_location
  ON products(fulfillment_mode, inventory_country_code, inventory_city, is_active, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_fulfillments_provider_status
  ON order_fulfillments(provider_id, status, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_fulfillment_events_ordered
  ON fulfillment_events(fulfillment_id, created_at, id);
CREATE INDEX IF NOT EXISTS idx_merchant_manifests_provider
  ON merchant_manifests(provider_id, status, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_merchant_manifest_events_ordered
  ON merchant_manifest_events(manifest_id, created_at, id);

-- Preserve all existing paid/history rows. Atlantic rows remain owned by the
-- platform; merchant rows start at pending and are operated by their seller.
INSERT INTO order_fulfillments(order_id, provider_id, route, owner, status, origin_snapshot, delivery_snapshot)
SELECT o.id, o.provider_id, o.fulfillment_mode,
       CASE WHEN o.fulfillment_mode='atlantic_import' THEN 'atlantic' ELSE 'merchant' END,
       CASE
         WHEN o.order_status::text IN ('Delivered','Completed') THEN 'delivered'
         WHEN o.current_tracking_stage::text='In Transit Internationally' THEN 'international_transit'
         WHEN o.current_tracking_stage::text='Arrived at Local Hub' THEN 'local_hub'
         ELSE 'pending'
       END,
       '{}'::jsonb, '{}'::jsonb
FROM orders o
ON CONFLICT(order_id) DO NOTHING;
