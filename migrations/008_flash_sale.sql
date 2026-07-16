-- Flash sale support
ALTER TABLE products
  ADD COLUMN IF NOT EXISTS is_flash_sale BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS flash_sale_price NUMERIC(14,2);

CREATE INDEX IF NOT EXISTS idx_products_flash_sale ON products(is_flash_sale, flash_sale_price)
  WHERE is_flash_sale = true;