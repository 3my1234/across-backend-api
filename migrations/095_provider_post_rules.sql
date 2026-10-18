UPDATE provider_subscription_plans SET listing_limit=CASE code WHEN 'provider-monthly' THEN 5 WHEN 'provider-growth' THEN 10 WHEN 'provider-business' THEN 20 WHEN 'provider-diamond' THEN 40 END,
 description=CASE code WHEN 'provider-monthly' THEN '5 posts total. Up to 2 images per post. No videos. Town Hall access. Limited visibility.' WHEN 'provider-growth' THEN '10 posts total. Up to 4 images per post. No videos. Town Hall access. Slightly limited visibility.' WHEN 'provider-business' THEN '20 posts total. Up to 8 images and 2 videos per post. Each video up to 60 seconds. Town Hall access. Increased visibility.' ELSE '40 posts total. Up to 8 images and 5 videos per post. Each video up to 60 seconds. Town Hall access. Increased visibility.' END,updated_at=now()
WHERE code IN ('provider-monthly','provider-growth','provider-business','provider-diamond');

CREATE TABLE provider_post_video_assets (
 url TEXT PRIMARY KEY,provider_id UUID NOT NULL REFERENCES provider_organizations(id) ON DELETE CASCADE,
 duration_seconds DOUBLE PRECISION NOT NULL CHECK(duration_seconds>0 AND duration_seconds<=60),
 size_bytes BIGINT NOT NULL CHECK(size_bytes>0 AND size_bytes<=52428800),created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE OR REPLACE FUNCTION enforce_provider_listing_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cap integer; used integer; provider uuid; tier text; max_images integer; max_videos integer; images jsonb; videos jsonb; item jsonb; previous jsonb; row_data jsonb;
BEGIN
 provider:=NEW.provider_id;IF provider IS NULL THEN RETURN NEW;END IF;
 PERFORM 1 FROM provider_organizations WHERE id=provider FOR UPDATE;
 SELECT listing_limit,badge_tier INTO cap,tier FROM provider_public_identity WHERE provider_id=provider;
 IF cap IS NULL THEN RAISE EXCEPTION 'Provider profile is required';END IF;
 row_data:=to_jsonb(NEW);previous:=CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
 IF TG_OP='INSERT' OR (COALESCE(previous->>'status',previous->>'moderation_status')='archived' AND COALESCE(row_data->>'status',row_data->>'moderation_status')<>'archived') THEN
  SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status<>'archived' AND id<>NEW.id)+(SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status<>'archived' AND id<>NEW.id) INTO used;
  IF used>=cap THEN RAISE EXCEPTION 'Your plan post limit has been reached. Products and services share one limit. Archive a post or upgrade.' USING ERRCODE='23514';END IF;
 END IF;
 max_images:=CASE tier WHEN 'bronze' THEN 2 WHEN 'silver' THEN 4 WHEN 'gold' THEN 8 WHEN 'diamond' THEN 8 ELSE 1 END;
 max_videos:=CASE tier WHEN 'gold' THEN 2 WHEN 'diamond' THEN 5 ELSE 0 END;
 images:=COALESCE(row_data->'image_urls',row_data->'media_urls','[]'::jsonb);
 videos:=COALESCE(row_data#>'{attributes,video_urls}',row_data#>'{factory_details,video_urls}','[]'::jsonb);
 -- Preserve existing records. New media or resubmission must meet today's plan.
 IF TG_OP='INSERT' OR row_data->'image_urls' IS DISTINCT FROM previous->'image_urls' OR row_data->'media_urls' IS DISTINCT FROM previous->'media_urls' OR row_data#>'{attributes,video_urls}' IS DISTINCT FROM previous#>'{attributes,video_urls}' OR row_data#>'{factory_details,video_urls}' IS DISTINCT FROM previous#>'{factory_details,video_urls}' OR (COALESCE(row_data->>'status',row_data->>'moderation_status') IN('pending','approved') AND COALESCE(row_data->>'status',row_data->>'moderation_status') IS DISTINCT FROM COALESCE(previous->>'status',previous->>'moderation_status')) THEN
  IF jsonb_typeof(images)<>'array' OR jsonb_array_length(images)>max_images THEN RAISE EXCEPTION 'Your plan allows at most % image(s) per post. Upgrade for more images.',max_images USING ERRCODE='23514';END IF;
  IF jsonb_typeof(videos)<>'array' OR jsonb_array_length(videos)>max_videos THEN RAISE EXCEPTION 'Your plan allows at most % video(s) per post. Upgrade to Gold or Diamond for videos.',max_videos USING ERRCODE='23514';END IF;
  FOR item IN SELECT value FROM jsonb_array_elements(videos) LOOP
   IF NOT EXISTS(SELECT 1 FROM provider_post_video_assets a WHERE a.provider_id=provider AND a.url=item#>>'{}' AND a.duration_seconds<=60) THEN RAISE EXCEPTION 'Your plan requires verified videos of no more than 60 seconds each. Upload the video again.' USING ERRCODE='23514';END IF;
  END LOOP;
 END IF;
 RETURN NEW;
END;$$;
DROP TRIGGER provider_product_capacity ON products;
CREATE TRIGGER provider_product_capacity BEFORE INSERT OR UPDATE ON products FOR EACH ROW EXECUTE FUNCTION enforce_provider_listing_capacity();
DROP TRIGGER provider_service_capacity ON provider_listings;
CREATE TRIGGER provider_service_capacity BEFORE INSERT OR UPDATE ON provider_listings FOR EACH ROW EXECUTE FUNCTION enforce_provider_listing_capacity();
