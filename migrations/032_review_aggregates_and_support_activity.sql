ALTER TABLE products
  ADD COLUMN IF NOT EXISTS review_count BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS review_rating_sum BIGINT NOT NULL DEFAULT 0;

UPDATE products p
SET review_count = aggregate.review_count,
    review_rating_sum = aggregate.review_rating_sum
FROM (
  SELECT product_id, COUNT(*)::bigint AS review_count, SUM(rating)::bigint AS review_rating_sum
  FROM reviews
  GROUP BY product_id
) aggregate
WHERE p.id = aggregate.product_id;

CREATE OR REPLACE FUNCTION maintain_product_review_aggregates()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    UPDATE products
    SET review_count = review_count + 1,
        review_rating_sum = review_rating_sum + NEW.rating
    WHERE id = NEW.product_id;
    RETURN NEW;
  END IF;

  IF TG_OP = 'UPDATE' THEN
    IF OLD.product_id = NEW.product_id THEN
      UPDATE products
      SET review_rating_sum = GREATEST(0, review_rating_sum + NEW.rating - OLD.rating)
      WHERE id = NEW.product_id;
    ELSE
      UPDATE products
      SET review_count = GREATEST(0, review_count - 1),
          review_rating_sum = GREATEST(0, review_rating_sum - OLD.rating)
      WHERE id = OLD.product_id;
      UPDATE products
      SET review_count = review_count + 1,
          review_rating_sum = review_rating_sum + NEW.rating
      WHERE id = NEW.product_id;
    END IF;
    RETURN NEW;
  END IF;

  UPDATE products
  SET review_count = GREATEST(0, review_count - 1),
      review_rating_sum = GREATEST(0, review_rating_sum - OLD.rating)
  WHERE id = OLD.product_id;
  RETURN OLD;
END;
$$;

DROP TRIGGER IF EXISTS trg_maintain_product_review_aggregates ON reviews;
CREATE TRIGGER trg_maintain_product_review_aggregates
AFTER INSERT OR UPDATE OF product_id, rating OR DELETE ON reviews
FOR EACH ROW EXECUTE FUNCTION maintain_product_review_aggregates();

-- Activity receipts now cover operational batch events and support-ticket
-- events. The UUID remains globally unique; removing the single-table FK lets
-- the same durable per-admin read model work for both event sources.
ALTER TABLE admin_activity_reads
  DROP CONSTRAINT IF EXISTS admin_activity_reads_event_id_fkey;

CREATE INDEX IF NOT EXISTS idx_support_tickets_activity_cursor
  ON support_tickets(created_at DESC, id DESC);
