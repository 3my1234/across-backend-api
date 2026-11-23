-- Cloudflare Stream provider videos: phones upload originals directly to Stream
-- (resumable tus); Stream encodes and delivers adaptive HLS. Additive only:
-- existing S3/FFmpeg assets, prices, plan counts and paid periods are unchanged.
-- The legacy S3 route keeps its own 180s / 50 MiB code limits.

-- Duration and size ceilings for Stream-hosted videos (10 minutes, 2 GiB originals).
ALTER TABLE provider_video_launch_policy DROP CONSTRAINT provider_video_launch_policy_max_seconds_check;
ALTER TABLE provider_video_launch_policy ADD CONSTRAINT provider_video_launch_policy_max_seconds_check CHECK(max_seconds BETWEEN 1 AND 600);
ALTER TABLE provider_video_launch_policy DROP CONSTRAINT provider_video_launch_policy_max_upload_bytes_check;
ALTER TABLE provider_video_launch_policy ADD CONSTRAINT provider_video_launch_policy_max_upload_bytes_check CHECK(max_upload_bytes BETWEEN 1048576 AND 2147483648);
ALTER TABLE provider_video_launch_policy ADD COLUMN stream_storage_reserve_minutes integer NOT NULL DEFAULT 30 CHECK(stream_storage_reserve_minutes BETWEEN 0 AND 100000);

ALTER TABLE provider_post_video_assets DROP CONSTRAINT provider_post_video_assets_duration_seconds_check;
ALTER TABLE provider_post_video_assets ADD CONSTRAINT provider_post_video_assets_duration_seconds_check CHECK(duration_seconds>0 AND duration_seconds<=600);
ALTER TABLE provider_post_video_assets DROP CONSTRAINT provider_post_video_assets_size_bytes_check;
ALTER TABLE provider_post_video_assets ADD CONSTRAINT provider_post_video_assets_size_bytes_check CHECK(size_bytes>0 AND size_bytes<=2147483648);
ALTER TABLE provider_post_video_assets ADD COLUMN stream_uid text UNIQUE;

ALTER TABLE provider_video_uploads DROP CONSTRAINT provider_video_uploads_size_bytes_check;
ALTER TABLE provider_video_uploads ADD CONSTRAINT provider_video_uploads_size_bytes_check CHECK(size_bytes BETWEEN 1 AND 2147483648);
ALTER TABLE provider_video_uploads DROP CONSTRAINT provider_video_uploads_processing_mode_check;
ALTER TABLE provider_video_uploads ADD CONSTRAINT provider_video_uploads_processing_mode_check CHECK(processing_mode IN('inline','background','stream'));
ALTER TABLE provider_video_uploads ADD COLUMN stream_uid text UNIQUE,
 ADD COLUMN stream_checked_at timestamptz,
 ADD COLUMN stream_deleted_at timestamptz;
CREATE INDEX provider_video_stream_open ON provider_video_uploads(created_at) WHERE processing_mode='stream' AND stream_deleted_at IS NULL;

-- Owner-approved 10-minute allowance and 1 GiB phone upload ceiling.
UPDATE provider_video_launch_policy SET max_seconds=600, max_upload_bytes=1073741824 WHERE singleton;
