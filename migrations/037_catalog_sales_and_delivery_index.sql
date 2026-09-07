ALTER TABLE products
    ADD COLUMN IF NOT EXISTS sold_count bigint NOT NULL DEFAULT 0;

ALTER TABLE products
    DROP CONSTRAINT IF EXISTS products_sold_count_nonnegative;

ALTER TABLE products
    ADD CONSTRAINT products_sold_count_nonnegative CHECK (sold_count >= 0);

UPDATE products p
SET sold_count = sales.sold_count
FROM (
    SELECT oi.product_id, SUM(oi.quantity)::bigint AS sold_count
    FROM order_items oi
    JOIN orders o ON o.id = oi.order_id
    WHERE oi.product_id IS NOT NULL
      AND o.paid_at IS NOT NULL
    GROUP BY oi.product_id
) sales
WHERE p.id = sales.product_id;

CREATE INDEX IF NOT EXISTS idx_orders_batch_tracking_created
    ON orders (batch_id, current_tracking_stage, created_at DESC, id DESC);
