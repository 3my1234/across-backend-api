-- Bounded, aggregate-only operational history. No customer content or identifiers.
CREATE TABLE ops_capacity_samples (
 host text NOT NULL CHECK(host IN('main','database')),
 observed_at timestamptz NOT NULL DEFAULT date_trunc('minute',clock_timestamp()),
 report jsonb NOT NULL CHECK(jsonb_typeof(report)='object' AND octet_length(report::text)<=65536),
 PRIMARY KEY(host,observed_at)
);
CREATE INDEX ops_capacity_recent ON ops_capacity_samples(observed_at DESC);
-- Diamond and Gold are the two video-enabled tiers. Keep existing count/size caps.
UPDATE provider_video_launch_policy SET diamond_enabled=true WHERE singleton;
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_functiondef('enforce_provider_listing_capacity()'::regprocedure) INTO definition;
 EXECUTE replace(definition,'Upgrade to Gold for new videos.','Upgrade to Diamond or Gold for new videos.');
END $$;
