-- Three-minute Gold videos. Preserve prices, counts, historical assets and Jobs slot semantics.
-- Deploy compatible API/worker validators before accepting the new duration on clients.
ALTER TABLE provider_video_launch_policy DROP CONSTRAINT provider_video_launch_policy_max_seconds_check;
ALTER TABLE provider_video_launch_policy ADD CONSTRAINT provider_video_launch_policy_max_seconds_check CHECK(max_seconds BETWEEN 1 AND 180);
UPDATE provider_video_launch_policy SET max_seconds=180 WHERE singleton;
ALTER TABLE provider_post_video_assets DROP CONSTRAINT provider_post_video_assets_duration_seconds_check;
ALTER TABLE provider_post_video_assets ADD CONSTRAINT provider_post_video_assets_duration_seconds_check CHECK(duration_seconds>0 AND duration_seconds<=180);

-- Remove the older hard-coded sixty-second guard without changing shared quota rules.
CREATE OR REPLACE FUNCTION enforce_provider_listing_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cap integer; used integer; provider uuid; tier text; max_images integer; max_videos integer; images jsonb; videos jsonb; item jsonb; previous jsonb; row_data jsonb;
BEGIN
 provider:=NEW.provider_id;IF provider IS NULL THEN RETURN NEW;END IF;
 IF TG_OP='UPDATE' AND NEW.provider_id IS DISTINCT FROM OLD.provider_id THEN RAISE EXCEPTION 'Your plan listing owner cannot be changed.' USING ERRCODE='23514';END IF;
 PERFORM 1 FROM provider_organizations WHERE id=provider FOR UPDATE;
 SELECT badge_tier INTO tier FROM provider_public_identity WHERE provider_id=provider;
 cap:=(provider_current_benefits(provider)->>'post_limit')::int;
 IF cap IS NULL THEN RAISE EXCEPTION 'Provider profile is required';END IF;
 row_data:=to_jsonb(NEW);previous:=CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
 IF TG_OP='INSERT' OR (COALESCE(previous->>'status',previous->>'moderation_status')='archived' AND COALESCE(row_data->>'status',row_data->>'moderation_status')<>'archived') THEN
  SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status<>'archived' AND id<>NEW.id)+(SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status<>'archived' AND id<>NEW.id)+(SELECT count(*) FROM provider_jobs WHERE provider_id=provider AND status='published' AND deadline>clock_timestamp()) INTO used;
  IF used>=cap THEN RAISE EXCEPTION 'Your plan post limit has been reached. Products, services and active published jobs share one limit. Archive a post or upgrade.' USING ERRCODE='23514';END IF;
 END IF;
 max_images:=(provider_tier_benefits(tier)->>'max_images')::int;
 max_videos:=(provider_tier_benefits(tier)->>'max_videos')::int;
 images:=COALESCE(row_data->'image_urls',row_data->'media_urls','[]'::jsonb);
 videos:=COALESCE(row_data#>'{attributes,video_urls}',row_data#>'{factory_details,video_urls}','[]'::jsonb);
 -- Preserve existing records. New media or resubmission must meet today's plan.
 IF TG_OP='INSERT' OR row_data->'image_urls' IS DISTINCT FROM previous->'image_urls' OR row_data->'media_urls' IS DISTINCT FROM previous->'media_urls' OR row_data#>'{attributes,video_urls}' IS DISTINCT FROM previous#>'{attributes,video_urls}' OR row_data#>'{factory_details,video_urls}' IS DISTINCT FROM previous#>'{factory_details,video_urls}' THEN
  IF jsonb_typeof(images)<>'array' OR (jsonb_array_length(images)>max_images AND (TG_OP='INSERT' OR NOT(images <@ COALESCE(previous->'image_urls',previous->'media_urls','[]'::jsonb)) OR jsonb_array_length(images)>jsonb_array_length(COALESCE(previous->'image_urls',previous->'media_urls','[]'::jsonb)))) THEN RAISE EXCEPTION 'Your plan allows at most % image(s) per post. Upgrade for more images.',max_images USING ERRCODE='23514';END IF;
  IF jsonb_typeof(videos)<>'array' OR (jsonb_array_length(videos)>max_videos AND (TG_OP='INSERT' OR NOT(videos <@ COALESCE(previous#>'{attributes,video_urls}',previous#>'{factory_details,video_urls}','[]'::jsonb)) OR jsonb_array_length(videos)>jsonb_array_length(COALESCE(previous#>'{attributes,video_urls}',previous#>'{factory_details,video_urls}','[]'::jsonb)))) THEN RAISE EXCEPTION 'Your plan allows at most % video(s) per post. Upgrade to Gold for new videos.',max_videos USING ERRCODE='23514';END IF;
  FOR item IN SELECT value FROM jsonb_array_elements(videos) WHERE NOT(COALESCE(previous#>'{attributes,video_urls}','[]'::jsonb) @> jsonb_build_array(value) OR COALESCE(previous#>'{factory_details,video_urls}','[]'::jsonb) @> jsonb_build_array(value)) OR TG_OP='INSERT' LOOP
   IF NOT EXISTS(SELECT 1 FROM provider_post_video_assets a WHERE a.provider_id=provider AND a.url=item#>>'{}' AND a.duration_seconds<=(provider_current_benefits(provider)->>'max_video_seconds')::int) THEN RAISE EXCEPTION 'Your plan requires verified videos within your current duration allowance. Upload the video again.' USING ERRCODE='23514';END IF;
  END LOOP;
 END IF;
 IF TG_OP='UPDATE' AND previous->>'published_at' IS NULL AND COALESCE(row_data->>'status',row_data->>'moderation_status') IN('pending','approved') AND COALESCE(row_data->>'status',row_data->>'moderation_status') IS DISTINCT FROM COALESCE(previous->>'status',previous->>'moderation_status') THEN
  SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status<>'archived' AND id<>NEW.id)+(SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status<>'archived' AND id<>NEW.id)+1+(SELECT count(*) FROM provider_jobs WHERE provider_id=provider AND status='published' AND deadline>clock_timestamp()) INTO used;
  IF used>cap THEN RAISE EXCEPTION 'Your plan listing limit is exceeded. Archive listings or upgrade before publishing another listing.' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NEW;
END;$$;
