-- Professional nearby discovery, provider reviews, and per-device sound preferences.
ALTER TABLE provider_listings
    ADD COLUMN IF NOT EXISTS review_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS review_rating_sum INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS provider_listing_reviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL UNIQUE REFERENCES provider_requests(id) ON DELETE CASCADE,
    listing_id UUID NOT NULL REFERENCES provider_listings(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rating SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
    review_text TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_provider_listing_reviews_listing_created
    ON provider_listing_reviews(listing_id, created_at DESC, id DESC);

CREATE OR REPLACE FUNCTION refresh_provider_listing_review_totals()
RETURNS TRIGGER AS $$
DECLARE target_listing UUID;
BEGIN
    target_listing := COALESCE(NEW.listing_id, OLD.listing_id);
    UPDATE provider_listings listing
       SET review_count = totals.review_count,
           review_rating_sum = totals.rating_sum,
           updated_at = now()
      FROM (
          SELECT COUNT(*)::integer AS review_count,
                 COALESCE(SUM(rating), 0)::integer AS rating_sum
            FROM provider_listing_reviews
           WHERE listing_id = target_listing
      ) totals
     WHERE listing.id = target_listing;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_provider_listing_review_totals ON provider_listing_reviews;
CREATE TRIGGER trg_provider_listing_review_totals
AFTER INSERT OR UPDATE OR DELETE ON provider_listing_reviews
FOR EACH ROW EXECUTE FUNCTION refresh_provider_listing_review_totals();

ALTER TABLE user_push_tokens
    ADD COLUMN IF NOT EXISTS sound_enabled BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE products
    ADD COLUMN IF NOT EXISTS inventory_latitude NUMERIC(9,6),
    ADD COLUMN IF NOT EXISTS inventory_longitude NUMERIC(9,6);

ALTER TABLE products DROP CONSTRAINT IF EXISTS products_inventory_coordinates_pair;
ALTER TABLE products ADD CONSTRAINT products_inventory_coordinates_pair CHECK (
    (inventory_latitude IS NULL AND inventory_longitude IS NULL) OR
    (inventory_latitude BETWEEN -90 AND 90 AND inventory_longitude BETWEEN -180 AND 180)
);

CREATE INDEX IF NOT EXISTS idx_products_inventory_coordinates
    ON products(inventory_latitude, inventory_longitude)
    WHERE inventory_latitude IS NOT NULL AND inventory_longitude IS NOT NULL;
