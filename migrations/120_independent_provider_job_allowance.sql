-- One free active Job for every verified provider, independent of product/service places.
-- Additive: preserve all existing jobs, history, conversations and product/service slots.
CREATE FUNCTION provider_active_job_limit() RETURNS integer LANGUAGE sql IMMUTABLE AS $$ SELECT 1 $$;
CREATE OR REPLACE FUNCTION provider_job_slot_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE used integer; verified boolean; retained boolean := false;
BEGIN
 IF TG_OP='UPDATE' AND NEW.provider_id<>OLD.provider_id THEN RAISE EXCEPTION 'Job owner cannot change' USING ERRCODE='23514';END IF;
 -- Serialize all publishes for this provider, including concurrent requests on different jobs.
 PERFORM 1 FROM provider_organizations WHERE id=NEW.provider_id FOR UPDATE;
 IF NEW.status='published' AND NEW.deadline>clock_timestamp() THEN
  SELECT is_active AND verification_status='approved' INTO verified FROM provider_organizations WHERE id=NEW.provider_id;
  IF verified IS DISTINCT FROM true THEN RAISE EXCEPTION 'Provider approval is required to publish jobs' USING ERRCODE='23514';END IF;
  SELECT count(*) INTO used FROM provider_jobs WHERE provider_id=NEW.provider_id AND id<>NEW.id AND status='published' AND deadline>clock_timestamp();
  -- Keep historical overages editable until their existing deadline, without granting extensions
  -- or additional publications. No migration rewrites or removes valid existing jobs.
  IF TG_OP='UPDATE' THEN retained := OLD.status='published' AND OLD.deadline>clock_timestamp() AND NEW.deadline<=OLD.deadline;END IF;
  IF used>=provider_active_job_limit() AND NOT retained THEN
   RAISE EXCEPTION 'Your free active job place is in use. Close or fill your current job, or wait for its deadline, before publishing another. Product and service places are separate.' USING ERRCODE='23514';
  END IF;
  NEW.published_at:=COALESCE(NEW.published_at,clock_timestamp());
  NEW.plan_slot:=1; -- Dedicated Jobs namespace; never competes with a product/service slot.
 ELSE NEW.plan_slot:=NULL;
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION provider_current_benefits(provider uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT provider_current_benefits_before_jobs(provider)||jsonb_build_object('active_job_limit',provider_active_job_limit(),'active_jobs_used',
 (SELECT count(*) FROM provider_jobs WHERE provider_id=provider AND status='published' AND deadline>clock_timestamp()));
$$;

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
  SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status<>'archived' AND id<>NEW.id)+(SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status<>'archived' AND id<>NEW.id) INTO used;
  IF used>=cap THEN RAISE EXCEPTION 'Your plan post limit has been reached. Products and services share one limit. Archive a post or upgrade.' USING ERRCODE='23514';END IF;
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
  SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status<>'archived' AND id<>NEW.id)+(SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status<>'archived' AND id<>NEW.id)+1 INTO used;
  IF used>cap THEN RAISE EXCEPTION 'Your plan listing limit is exceeded. Archive listings or upgrade before publishing another listing.' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NEW;
END;$$;

CREATE OR REPLACE FUNCTION assign_provider_post_slot() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE provider uuid; cap integer; assigned integer; row_data jsonb; previous jsonb;
BEGIN
 provider:=NEW.provider_id;IF provider IS NULL THEN RETURN NEW;END IF;
 row_data:=to_jsonb(NEW);previous:=CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
 IF TG_OP='UPDATE' AND NEW.plan_slot IS DISTINCT FROM OLD.plan_slot THEN RAISE EXCEPTION 'Your plan slot cannot be changed directly.' USING ERRCODE='23514';END IF;
 IF TG_OP='INSERT' OR (COALESCE(previous->>'status',previous->>'moderation_status')='archived' AND COALESCE(row_data->>'status',row_data->>'moderation_status')<>'archived') THEN
  PERFORM 1 FROM provider_organizations WHERE id=provider FOR UPDATE;
  SELECT (provider_current_benefits(provider)->>'post_limit')::int INTO cap;
  SELECT n INTO assigned FROM generate_series(1,cap) n WHERE NOT EXISTS(SELECT 1 FROM products p WHERE p.provider_id=provider AND p.moderation_status<>'archived' AND p.id<>NEW.id AND p.plan_slot=n) AND NOT EXISTS(SELECT 1 FROM provider_listings l WHERE l.provider_id=provider AND l.status<>'archived' AND l.id<>NEW.id AND l.plan_slot=n) ORDER BY n LIMIT 1;
  IF assigned IS NULL THEN RAISE EXCEPTION 'Your plan post allowance is full. Archive a post or upgrade.' USING ERRCODE='23514';END IF;
  NEW.plan_slot:=assigned;
 END IF;
 RETURN NEW;
END;$$;

