-- Owner-approved stricter NEW uploads only. Existing media and paid records stay intact.
-- No S3 deletion, plan-price change, or rewrite of existing message/notification state.
CREATE TABLE provider_video_launch_policy (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 diamond_enabled boolean NOT NULL DEFAULT false,
 videos_per_listing integer NOT NULL DEFAULT 1 CHECK(videos_per_listing BETWEEN 1 AND 5),
 videos_per_provider integer NOT NULL DEFAULT 5 CHECK(videos_per_provider BETWEEN 1 AND 100),
 max_seconds integer NOT NULL DEFAULT 30 CHECK(max_seconds BETWEEN 1 AND 60),
 max_upload_bytes bigint NOT NULL DEFAULT 52428800 CHECK(max_upload_bytes BETWEEN 1048576 AND 52428800),
 max_provider_stored_bytes bigint NOT NULL DEFAULT 262144000 CHECK(max_provider_stored_bytes BETWEEN 52428800 AND 10737418240),
 daily_upload_limit integer NOT NULL DEFAULT 10 CHECK(daily_upload_limit BETWEEN 1 AND 100),
 pending_upload_limit integer NOT NULL DEFAULT 2 CHECK(pending_upload_limit BETWEEN 1 AND 10),
 uploads_paused boolean NOT NULL DEFAULT false
);
INSERT INTO provider_video_launch_policy(singleton) VALUES(true);
CREATE OR REPLACE FUNCTION provider_tier_benefits(tier text) RETURNS jsonb LANGUAGE sql STABLE AS $$
SELECT jsonb_build_object('post_limit',CASE tier WHEN 'bronze' THEN 3 WHEN 'silver' THEN 10 WHEN 'diamond' THEN 25 WHEN 'gold' THEN 60 ELSE 1 END,
 'max_images',CASE tier WHEN 'bronze' THEN 3 WHEN 'silver' THEN 5 WHEN 'diamond' THEN 8 WHEN 'gold' THEN 12 ELSE 1 END,
 'max_videos',CASE WHEN tier='gold' OR (tier='diamond' AND policy.diamond_enabled) THEN policy.videos_per_listing ELSE 0 END,
 'max_video_seconds',policy.max_seconds,'max_provider_videos',policy.videos_per_provider,
 'max_video_bytes',policy.max_upload_bytes,'video_uploads_paused',policy.uploads_paused,
 'town_hall',tier IN('bronze','silver','diamond','gold'),
 'town_hall_room_limit',CASE tier WHEN 'bronze' THEN 3 WHEN 'silver' THEN 5 WHEN 'diamond' THEN 8 WHEN 'gold' THEN 10 ELSE 0 END,
 'promotional_weight',CASE tier WHEN 'silver' THEN 2 WHEN 'diamond' THEN 3 WHEN 'gold' THEN 5 ELSE 1 END,
 'visibility',CASE tier WHEN 'bronze' THEN 'basic' WHEN 'silver' THEN 'standard' WHEN 'diamond' THEN 'enhanced' WHEN 'gold' THEN 'premium' ELSE 'limited' END,
 'combined_limit',true,'direct_messaging',true,'recommended',true,'town_hall_search',tier IN('bronze','silver','diamond','gold')) FROM provider_video_launch_policy policy WHERE singleton;
$$;

CREATE FUNCTION enforce_provider_video_launch_policy() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE provider uuid; row_data jsonb; previous jsonb; videos jsonb; old_videos jsonb;
 added boolean; active_now boolean; was_active boolean; used integer; allowance jsonb; item jsonb;
BEGIN
 provider:=NEW.provider_id; IF provider IS NULL THEN RETURN NEW; END IF;
 row_data:=to_jsonb(NEW); previous:=CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
 videos:=COALESCE(row_data#>'{factory_details,video_urls}',row_data#>'{attributes,video_urls}','[]'::jsonb);
 old_videos:=COALESCE(previous#>'{factory_details,video_urls}',previous#>'{attributes,video_urls}','[]'::jsonb);
 IF jsonb_typeof(videos)<>'array' THEN RAISE EXCEPTION 'Your plan requires a video array.' USING ERRCODE='23514'; END IF;
 active_now:=COALESCE(row_data->>'moderation_status',row_data->>'status')<>'archived';
 was_active:=TG_OP='UPDATE' AND COALESCE(previous->>'moderation_status',previous->>'status')<>'archived';
 SELECT EXISTS(SELECT 1 FROM jsonb_array_elements(videos) v WHERE NOT(old_videos @> jsonb_build_array(v))) INTO added;
 -- Preserve unchanged/reordered/removed historical videos, including on paid downgrades.
 IF NOT added AND (was_active OR NOT active_now OR jsonb_array_length(videos)=0) THEN RETURN NEW; END IF;
 PERFORM 1 FROM provider_organizations WHERE id=provider FOR UPDATE;
 allowance:=provider_current_benefits(provider);
 IF COALESCE((allowance->>'video_uploads_paused')::boolean,true) THEN RAISE EXCEPTION 'Your plan video uploads are temporarily paused.' USING ERRCODE='23514'; END IF;
 IF COALESCE((allowance->>'max_videos')::integer,0)=0 OR jsonb_array_length(videos)>(allowance->>'max_videos')::integer THEN
  RAISE EXCEPTION 'Your plan permits new videos on Gold only, one per listing.' USING ERRCODE='23514'; END IF;
 FOR item IN SELECT value FROM jsonb_array_elements(videos) LOOP
  IF NOT(old_videos @> jsonb_build_array(item)) OR NOT was_active THEN
   IF NOT EXISTS(SELECT 1 FROM provider_post_video_assets a WHERE a.provider_id=provider AND a.url=item#>>'{}'
      AND a.duration_seconds<=(allowance->>'max_video_seconds')::integer AND a.size_bytes<=(allowance->>'max_video_bytes')::bigint) THEN
    RAISE EXCEPTION 'Your plan requires verified videos within the current duration and size limits.' USING ERRCODE='23514'; END IF;
  END IF;
 END LOOP;
 IF active_now THEN
  SELECT COALESCE((SELECT sum(jsonb_array_length(COALESCE(factory_details->'video_urls','[]'::jsonb))) FROM products WHERE provider_id=provider AND id<>NEW.id AND moderation_status<>'archived'),0)
       + COALESCE((SELECT sum(jsonb_array_length(COALESCE(attributes->'video_urls','[]'::jsonb))) FROM provider_listings WHERE provider_id=provider AND id<>NEW.id AND status<>'archived'),0)
       + jsonb_array_length(videos) INTO used;
  IF used>(allowance->>'max_provider_videos')::integer THEN RAISE EXCEPTION 'Your plan provider-wide active video allowance is full. Remove a video before adding another.' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER provider_product_video_launch BEFORE INSERT OR UPDATE ON products FOR EACH ROW EXECUTE FUNCTION enforce_provider_video_launch_policy();
CREATE TRIGGER provider_service_video_launch BEFORE INSERT OR UPDATE ON provider_listings FOR EACH ROW EXECUTE FUNCTION enforce_provider_video_launch_policy();

-- Reservations count before a signed URL is released; terminal rows retain abuse accounting.
CREATE TABLE provider_video_uploads (
 key text PRIMARY KEY, provider_id uuid NOT NULL REFERENCES provider_organizations(id),
 asset_id uuid NOT NULL UNIQUE, size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 52428800),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','processing','complete')),
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL DEFAULT now()+interval '20 minutes',
 lease_until timestamptz, url text, duration_seconds double precision,
 CHECK(state<>'complete' OR (url IS NOT NULL AND duration_seconds>0))
);
CREATE INDEX provider_video_upload_daily ON provider_video_uploads(provider_id,created_at);
CREATE INDEX provider_video_upload_pending ON provider_video_uploads(provider_id,expires_at) WHERE state<>'complete';
CREATE INDEX provider_video_assets_owner ON provider_post_video_assets(provider_id);
