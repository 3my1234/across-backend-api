-- Search and keyset-pagination indexes for admin datasets.
-- Trigram indexes keep case-insensitive partial searches index-backed as data grows.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_admins_created_id_desc
  ON admins(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_admins_email_trgm
  ON admins USING gin (email gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_admins_full_name_trgm
  ON admins USING gin (full_name gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_users_created_id_desc
  ON users(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_users_email_trgm
  ON users USING gin (email gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_users_full_name_trgm
  ON users USING gin (full_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_users_phone_trgm
  ON users USING gin (phone gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_orders_created_id_desc
  ON orders(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_orders_paid_created
  ON orders(paid_at DESC) WHERE paid_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_orders_batch_created_id
  ON orders(batch_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_orders_flutterwave_ref_trgm
  ON orders USING gin (flutterwave_tx_ref gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_products_created_id_desc
  ON products(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_products_sku_trgm
  ON products USING gin (sku gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_products_title_trgm
  ON products USING gin (title gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_batches_created_id_desc
  ON order_batches(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_batches_code_trgm
  ON order_batches USING gin (batch_code gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_batches_location_trgm
  ON order_batches USING gin (current_location gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_order_items_order_created_id
  ON order_items(order_id, created_at DESC, id DESC);
