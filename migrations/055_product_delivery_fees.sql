-- Keep existing seller totals unchanged; old offers have delivery included.
ALTER TABLE product_delivery_areas
  ADD COLUMN IF NOT EXISTS delivery_fee NUMERIC(14,2) NOT NULL DEFAULT 0
    CHECK (delivery_fee >= 0 AND delivery_fee < delivered_price);
