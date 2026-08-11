CREATE INDEX IF NOT EXISTS idx_orders_package_label_trgm
  ON orders USING gin (package_label gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_order_items_sku_trgm
  ON order_items USING gin (sku gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_order_items_title_trgm
  ON order_items USING gin (title gin_trgm_ops);
