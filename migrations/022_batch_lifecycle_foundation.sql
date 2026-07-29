ALTER TABLE countries_config
  ADD COLUMN IF NOT EXISTS operational_timezone TEXT NOT NULL DEFAULT 'UTC';

UPDATE countries_config
SET operational_timezone = 'Africa/Lagos'
WHERE country_code = 'NG'
  AND operational_timezone = 'UTC';

ALTER TABLE order_batches
  DROP CONSTRAINT IF EXISTS order_batches_country_id_batch_date_key;

ALTER TABLE order_batches
  ADD COLUMN IF NOT EXISTS route_key TEXT NOT NULL DEFAULT 'LOS',
  ADD COLUMN IF NOT EXISTS batch_sequence INTEGER NOT NULL DEFAULT 1 CHECK (batch_sequence > 0),
  ADD COLUMN IF NOT EXISTS opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS membership_locked BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  ADD COLUMN IF NOT EXISTS reconciled_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS reconciled_by UUID,
  ADD COLUMN IF NOT EXISTS procurement_funds_amount NUMERIC(14,2)
    CHECK (procurement_funds_amount IS NULL OR procurement_funds_amount > 0),
  ADD COLUMN IF NOT EXISTS procurement_funds_currency CHAR(3) NOT NULL DEFAULT 'NGN',
  ADD COLUMN IF NOT EXISTS procurement_funds_reference TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS procurement_funds_sent_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS procurement_funds_sent_by UUID,
  ADD COLUMN IF NOT EXISTS procurement_funds_acknowledged_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS procurement_funds_acknowledged_by UUID,
  ADD COLUMN IF NOT EXISTS procurement_completed_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS procurement_completed_by UUID,
  ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;

UPDATE order_batches
SET opened_at = created_at,
    membership_locked = status <> 'collecting_funds'::batch_status,
    closed_at = CASE
      WHEN status <> 'collecting_funds'::batch_status THEN COALESCE(closed_at, updated_at)
      ELSE closed_at
    END,
    completed_at = CASE
      WHEN status = 'completed'::batch_status THEN COALESCE(completed_at, updated_at)
      ELSE completed_at
    END;

UPDATE order_batches
SET status = 'funds_sent_to_procurement'::batch_status,
    procurement_funds_amount = COALESCE(procurement_funds_amount, NULLIF(total_ngn_collected, 0)),
    procurement_funds_reference = CASE
      WHEN procurement_funds_reference = '' THEN 'legacy-status-migration'
      ELSE procurement_funds_reference
    END,
    procurement_funds_sent_at = COALESCE(procurement_funds_sent_at, updated_at),
    membership_locked = true
WHERE status = 'funds_sent_to_china'::batch_status;

UPDATE order_batches
SET status = 'ready_for_pickup'::batch_status,
    membership_locked = true
WHERE status = 'sorted'::batch_status;

CREATE UNIQUE INDEX IF NOT EXISTS order_batches_operational_slot_unique
  ON order_batches(country_id, batch_date, route_key, transport_mode, batch_sequence);

CREATE INDEX IF NOT EXISTS idx_batches_open_business_day
  ON order_batches(country_id, batch_date, route_key, transport_mode, batch_sequence DESC)
  WHERE membership_locked = false AND status = 'collecting_funds'::batch_status;

CREATE INDEX IF NOT EXISTS idx_batches_closure_due
  ON order_batches(batch_date, country_id)
  WHERE membership_locked = false AND status = 'collecting_funds'::batch_status;

ALTER TABLE batch_events
  ADD COLUMN IF NOT EXISTS previous_status batch_status,
  ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_batch_events_batch_created_id
  ON batch_events(batch_id, created_at DESC, id DESC);

ALTER TABLE order_items
  ADD COLUMN IF NOT EXISTS exception_resolution TEXT NOT NULL DEFAULT 'none'
    CHECK (exception_resolution IN ('none', 'pending', 'refunded', 'substituted', 'cancelled')),
  ADD COLUMN IF NOT EXISTS exception_resolved_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS exception_resolved_by UUID;

CREATE INDEX IF NOT EXISTS idx_order_items_batch_procurement
  ON order_items(purchase_status, exception_resolution, order_id);
