-- Serialize atomic deltas on the listing row. Full recomputation inside a row
-- trigger could lose a concurrent review's totals under READ COMMITTED.
LOCK TABLE provider_listing_reviews IN SHARE ROW EXCLUSIVE MODE;
CREATE OR REPLACE FUNCTION refresh_provider_listing_review_totals()
RETURNS TRIGGER AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  UPDATE provider_listings SET review_count=review_count+1,
   review_rating_sum=review_rating_sum+NEW.rating,updated_at=now() WHERE id=NEW.listing_id;
 ELSIF TG_OP='DELETE' THEN
  UPDATE provider_listings SET review_count=review_count-1,
   review_rating_sum=review_rating_sum-OLD.rating,updated_at=now() WHERE id=OLD.listing_id;
 ELSIF NEW.listing_id=OLD.listing_id THEN
  UPDATE provider_listings SET review_rating_sum=review_rating_sum+NEW.rating-OLD.rating,
   updated_at=now() WHERE id=NEW.listing_id;
 ELSE
  PERFORM id FROM provider_listings WHERE id IN (OLD.listing_id,NEW.listing_id) ORDER BY id FOR UPDATE;
  UPDATE provider_listings SET review_count=review_count-1,
   review_rating_sum=review_rating_sum-OLD.rating,updated_at=now() WHERE id=OLD.listing_id;
  UPDATE provider_listings SET review_count=review_count+1,
   review_rating_sum=review_rating_sum+NEW.rating,updated_at=now() WHERE id=NEW.listing_id;
 END IF;
 RETURN NULL;
END;
$$ LANGUAGE plpgsql;

UPDATE provider_listings listing SET
 review_count=(SELECT count(*) FROM provider_listing_reviews WHERE listing_id=listing.id),
 review_rating_sum=(SELECT COALESCE(sum(rating),0) FROM provider_listing_reviews WHERE listing_id=listing.id);
