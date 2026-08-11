ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS fulfillment_contact_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE orders order_row
SET fulfillment_contact_snapshot = jsonb_build_object(
  'full_name', COALESCE(buyer.full_name, ''),
  'email', COALESCE(buyer.email::text, ''),
  'phone', COALESCE(buyer.phone, ''),
  'address', COALESCE(buyer.address, ''),
  'city', COALESCE(buyer.city, ''),
  'state', COALESCE(buyer.state, ''),
  'postal_code', COALESCE(buyer.postal_code, '')
)
FROM users buyer
WHERE buyer.id = order_row.user_id
  AND order_row.fulfillment_contact_snapshot = '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_orders_fulfillment_contact_trgm
  ON orders USING gin ((fulfillment_contact_snapshot::text) gin_trgm_ops);
