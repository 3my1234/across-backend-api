-- Owner-approved 2026-11-23: per-tier video allowances and a monthly watch budget.
-- Diamond: 2 videos per listing, 10 active videos, 2 minutes each, ~1,000 watched minutes/month.
-- Gold:    3 videos per listing, 20 active videos, 3 minutes each, ~2,000 watched minutes/month.
-- Existing videos and listings are preserved (the triggers only check NEW videos).
-- Values are policy columns so they can be tuned without a code release.
ALTER TABLE provider_video_launch_policy
 ADD COLUMN diamond_videos_per_listing integer NOT NULL DEFAULT 2 CHECK(diamond_videos_per_listing BETWEEN 1 AND 5),
 ADD COLUMN gold_videos_per_listing integer NOT NULL DEFAULT 3 CHECK(gold_videos_per_listing BETWEEN 1 AND 5),
 ADD COLUMN diamond_provider_videos integer NOT NULL DEFAULT 10 CHECK(diamond_provider_videos BETWEEN 1 AND 100),
 ADD COLUMN gold_provider_videos integer NOT NULL DEFAULT 20 CHECK(gold_provider_videos BETWEEN 1 AND 100),
 ADD COLUMN diamond_max_seconds integer NOT NULL DEFAULT 120 CHECK(diamond_max_seconds BETWEEN 1 AND 600),
 ADD COLUMN gold_max_seconds integer NOT NULL DEFAULT 180 CHECK(gold_max_seconds BETWEEN 1 AND 600),
 ADD COLUMN diamond_watch_minutes integer NOT NULL DEFAULT 1000 CHECK(diamond_watch_minutes >= 0),
 ADD COLUMN gold_watch_minutes integer NOT NULL DEFAULT 2000 CHECK(gold_watch_minutes >= 0);

CREATE OR REPLACE FUNCTION provider_tier_benefits(tier text) RETURNS jsonb LANGUAGE sql STABLE AS $$
SELECT jsonb_build_object('post_limit',CASE tier WHEN 'bronze' THEN 3 WHEN 'silver' THEN 10 WHEN 'diamond' THEN 25 WHEN 'gold' THEN 60 ELSE 1 END,
 'max_images',CASE tier WHEN 'bronze' THEN 3 WHEN 'silver' THEN 5 WHEN 'diamond' THEN 8 WHEN 'gold' THEN 12 ELSE 1 END,
 'max_videos',CASE WHEN tier='gold' THEN policy.gold_videos_per_listing WHEN tier='diamond' AND policy.diamond_enabled THEN policy.diamond_videos_per_listing ELSE 0 END,
 'max_video_seconds',CASE tier WHEN 'gold' THEN policy.gold_max_seconds WHEN 'diamond' THEN policy.diamond_max_seconds ELSE LEAST(policy.max_seconds,180) END,
 'max_provider_videos',CASE tier WHEN 'gold' THEN policy.gold_provider_videos WHEN 'diamond' THEN policy.diamond_provider_videos ELSE policy.videos_per_provider END,
 'video_watch_minutes',CASE tier WHEN 'gold' THEN policy.gold_watch_minutes WHEN 'diamond' THEN policy.diamond_watch_minutes ELSE 0 END,
 'max_video_bytes',policy.max_upload_bytes,'video_uploads_paused',policy.uploads_paused,
 'town_hall',tier IN('bronze','silver','diamond','gold'),
 'town_hall_room_limit',CASE tier WHEN 'bronze' THEN 3 WHEN 'silver' THEN 5 WHEN 'diamond' THEN 8 WHEN 'gold' THEN 10 ELSE 0 END,
 'promotional_weight',CASE tier WHEN 'silver' THEN 2 WHEN 'diamond' THEN 3 WHEN 'gold' THEN 5 ELSE 1 END,
 'visibility',CASE tier WHEN 'bronze' THEN 'basic' WHEN 'silver' THEN 'standard' WHEN 'diamond' THEN 'enhanced' WHEN 'gold' THEN 'premium' ELSE 'limited' END,
 'combined_limit',true,'direct_messaging',true,'recommended',true,'town_hall_search',tier IN('bronze','silver','diamond','gold')) FROM provider_video_launch_policy policy WHERE singleton;
$$;

-- Watched video time from the in-app viewer, cumulative per session/listing/day.
-- Feeds the per-provider monthly watch budget (over budget -> tap-to-play, no autoplay).
CREATE TABLE discovery_video_watch (
 session_hash text NOT NULL,
 provider_id uuid NOT NULL REFERENCES provider_organizations(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK(kind IN ('product','service')),
 listing_id uuid NOT NULL,
 day date NOT NULL,
 watched_ms bigint NOT NULL CHECK(watched_ms BETWEEN 0 AND 3600000),
 PRIMARY KEY(session_hash,kind,listing_id,day)
);
CREATE INDEX discovery_video_watch_provider_day ON discovery_video_watch(provider_id,day);
