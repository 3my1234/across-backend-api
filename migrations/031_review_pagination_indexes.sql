CREATE INDEX IF NOT EXISTS idx_reviews_product_cursor
  ON reviews(product_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_reviews_product_rating
  ON reviews(product_id, rating);
