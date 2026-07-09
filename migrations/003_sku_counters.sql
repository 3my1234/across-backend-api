CREATE TABLE IF NOT EXISTS sku_counters (
  category_code TEXT PRIMARY KEY,
  next_value INTEGER NOT NULL CHECK (next_value > 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
