ALTER TABLE products ADD COLUMN IF NOT EXISTS plan_slot integer;
ALTER TABLE provider_listings ADD COLUMN IF NOT EXISTS plan_slot integer;
-- Allocate stable slots across both post types once. Existing content is kept.
WITH ranked AS (
 SELECT kind,id,row_number() OVER(PARTITION BY provider_id ORDER BY approved DESC NULLS LAST,posted_at,kind,id)::int AS slot FROM (
 SELECT 'product' AS kind,id,provider_id,moderation_status='approved' AS approved,COALESCE(published_at,(to_jsonb(p)->>'created_at')::timestamptz,'epoch'::timestamptz) AS posted_at FROM products p WHERE provider_id IS NOT NULL AND moderation_status<>'archived'
 UNION ALL SELECT 'service',id,provider_id,status='approved',COALESCE(published_at,(to_jsonb(l)->>'created_at')::timestamptz,'epoch'::timestamptz) FROM provider_listings l WHERE status<>'archived') all_posts)
UPDATE products p SET plan_slot=r.slot FROM ranked r WHERE r.kind='product' AND r.id=p.id;
WITH ranked AS (
 SELECT kind,id,row_number() OVER(PARTITION BY provider_id ORDER BY approved DESC NULLS LAST,posted_at,kind,id)::int AS slot FROM (
 SELECT 'product' AS kind,id,provider_id,moderation_status='approved' AS approved,COALESCE(published_at,(to_jsonb(p)->>'created_at')::timestamptz,'epoch'::timestamptz) AS posted_at FROM products p WHERE provider_id IS NOT NULL AND moderation_status<>'archived'
 UNION ALL SELECT 'service',id,provider_id,status='approved',COALESCE(published_at,(to_jsonb(l)->>'created_at')::timestamptz,'epoch'::timestamptz) FROM provider_listings l WHERE status<>'archived') all_posts)
UPDATE provider_listings l SET plan_slot=r.slot FROM ranked r WHERE r.kind='service' AND r.id=l.id;
CREATE INDEX provider_product_plan_slots ON products(provider_id,plan_slot) WHERE moderation_status<>'archived';
CREATE INDEX provider_service_plan_slots ON provider_listings(provider_id,plan_slot) WHERE status<>'archived';
CREATE OR REPLACE FUNCTION enforce_provider_listing_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cap integer; used integer; provider uuid; tier text; max_images integer; max_videos integer; images jsonb; videos jsonb; item jsonb; previous jsonb; row_data jsonb;
BEGIN
 provider:=NEW.provider_id;IF provider IS NULL THEN RETURN NEW;END IF;
 PERFORM 1 FROM provider_organizations WHERE id=provider FOR UPDATE;
 SELECT listing_limit,badge_tier INTO cap,tier FROM provider_public_identity WHERE provider_id=provider;
 IF cap IS NULL THEN RAISE EXCEPTION 'Provider profile is required';END IF;
 row_data:=to_jsonb(NEW);previous:=CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
 IF TG_OP='INSERT' OR (COALESCE(previous->>'status',previous->>'moderation_status')='archived' AND COALESCE(row_data->>'status',row_data->>'moderation_status')<>'archived') THEN
  SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status<>'archived' AND plan_slot<=cap AND id<>NEW.id)+(SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status<>'archived' AND plan_slot<=cap AND id<>NEW.id) INTO used;
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
CREATE FUNCTION assign_provider_post_slot() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE provider uuid; cap integer; assigned integer; row_data jsonb; previous jsonb;
BEGIN
 provider:=NEW.provider_id;IF provider IS NULL THEN RETURN NEW;END IF;
 row_data:=to_jsonb(NEW);previous:=CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
 IF TG_OP='UPDATE' AND NEW.plan_slot IS DISTINCT FROM OLD.plan_slot THEN RAISE EXCEPTION 'Your plan slot cannot be changed directly.' USING ERRCODE='23514';END IF;
 IF TG_OP='INSERT' OR (COALESCE(previous->>'status',previous->>'moderation_status')='archived' AND COALESCE(row_data->>'status',row_data->>'moderation_status')<>'archived') THEN
  PERFORM 1 FROM provider_organizations WHERE id=provider FOR UPDATE;
  SELECT listing_limit INTO cap FROM provider_public_identity WHERE provider_id=provider;
  SELECT n INTO assigned FROM generate_series(1,cap) n WHERE NOT EXISTS(SELECT 1 FROM products p WHERE p.provider_id=provider AND p.moderation_status<>'archived' AND p.id<>NEW.id AND p.plan_slot=n) AND NOT EXISTS(SELECT 1 FROM provider_listings l WHERE l.provider_id=provider AND l.status<>'archived' AND l.id<>NEW.id AND l.plan_slot=n) ORDER BY n LIMIT 1;
  IF assigned IS NULL THEN RAISE EXCEPTION 'Your plan post allowance is full. Archive a post or upgrade.' USING ERRCODE='23514';END IF;
  NEW.plan_slot:=assigned;
 END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER provider_product_slot BEFORE INSERT OR UPDATE ON products FOR EACH ROW EXECUTE FUNCTION assign_provider_post_slot();
CREATE TRIGGER provider_service_slot BEFORE INSERT OR UPDATE ON provider_listings FOR EACH ROW EXECUTE FUNCTION assign_provider_post_slot();
