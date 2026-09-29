-- Atlantic Express is the marketplace operator, not the product importer or
-- delivery carrier. Existing legacy orders remain readable for audit/history,
-- while new catalogue and checkout activity is seller-managed only.

UPDATE products
SET is_active = false,
    atlantic_last_mile = false,
    updated_at = now()
WHERE provider_id IS NULL
   OR fulfillment_mode = 'atlantic_import'
   OR atlantic_last_mile = true;

ALTER TABLE products
  ALTER COLUMN fulfillment_mode SET DEFAULT 'merchant_local';

ALTER TABLE orders
  ALTER COLUMN fulfillment_mode SET DEFAULT 'merchant_local';

ALTER TABLE order_items
  ALTER COLUMN fulfillment_mode SET DEFAULT 'merchant_local';

UPDATE admins
SET role = 'catalog_admin',
    updated_at = now()
WHERE role IN ('admin', 'procurement_admin', 'courier_admin');

ALTER TABLE admins DROP CONSTRAINT IF EXISTS admins_role_check;
ALTER TABLE admins
  ADD CONSTRAINT admins_role_check
  CHECK (role IN ('super_admin', 'catalog_admin'));
