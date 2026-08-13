UPDATE products
SET is_flash_sale = false, flash_sale_price = NULL
WHERE is_flash_sale = true
  AND (flash_sale_price IS NULL OR flash_sale_price <= 0 OR flash_sale_price >= local_selling_price);

UPDATE products SET flash_sale_price = NULL WHERE is_flash_sale = false;

ALTER TABLE products DROP CONSTRAINT IF EXISTS products_flash_sale_price_check;
ALTER TABLE products ADD CONSTRAINT products_flash_sale_price_check CHECK (
  (is_flash_sale = false AND flash_sale_price IS NULL)
  OR
  (is_flash_sale = true AND flash_sale_price > 0 AND flash_sale_price < local_selling_price)
);

CREATE INDEX IF NOT EXISTS idx_products_active_flash_cursor
  ON products(created_at DESC, id DESC)
  WHERE is_active = true AND is_flash_sale = true AND inventory_count > 0;

CREATE INDEX IF NOT EXISTS idx_products_description_trgm
  ON products USING gin (description gin_trgm_ops);
