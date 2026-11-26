-- Customers who were answered in chat may review a service, not only after a completed
-- booking. Such a review has no request; each customer keeps one per service.
ALTER TABLE provider_listing_reviews ALTER COLUMN request_id DROP NOT NULL;
CREATE UNIQUE INDEX provider_listing_review_from_chat ON provider_listing_reviews(listing_id,user_id) WHERE request_id IS NULL;
