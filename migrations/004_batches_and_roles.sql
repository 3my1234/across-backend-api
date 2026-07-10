CREATE TYPE batch_status AS ENUM (
  'collecting_funds',
  'settled',
  'funds_sent_to_china',
  'purchasing',
  'enroute_nigeria',
  'arrived_local',
  'sorted',
  'completed'
);

CREATE TABLE IF NOT EXISTS order_batches (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_code TEXT NOT NULL UNIQUE,
  country_id UUID NOT NULL REFERENCES countries_config(id),
  batch_date DATE NOT NULL,
  status batch_status NOT NULL DEFAULT 'collecting_funds',
  transport_mode TEXT NOT NULL DEFAULT 'air' CHECK (transport_mode IN ('air', 'sea')),
  total_ngn_collected NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (total_ngn_collected >= 0),
  total_cny_sent NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (total_cny_sent >= 0),
  current_location TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(country_id, batch_date)
);

CREATE TABLE IF NOT EXISTS batch_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id UUID NOT NULL REFERENCES order_batches(id) ON DELETE CASCADE,
  actor_id UUID,
  event_type TEXT NOT NULL,
  status batch_status,
  location TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS batch_id UUID,
  ADD COLUMN IF NOT EXISTS package_label TEXT;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'orders_batch_id_fkey'
  ) THEN
    ALTER TABLE orders
      ADD CONSTRAINT orders_batch_id_fkey FOREIGN KEY (batch_id) REFERENCES order_batches(id);
  END IF;
END $$;

ALTER TABLE admins
  DROP CONSTRAINT IF EXISTS admins_role_check;

ALTER TABLE admins
  ADD CONSTRAINT admins_role_check CHECK (role IN ('super_admin', 'admin', 'catalog_admin', 'procurement_admin', 'courier_admin'));

CREATE INDEX IF NOT EXISTS idx_batches_date_status ON order_batches(batch_date, status);
CREATE INDEX IF NOT EXISTS idx_batch_events_batch_time ON batch_events(batch_id, created_at);
