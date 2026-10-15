CREATE TABLE provider_driver_presence (
 provider_id uuid PRIMARY KEY REFERENCES provider_organizations(id) ON DELETE CASCADE,
 listing_id uuid NOT NULL REFERENCES provider_listings(id) ON DELETE CASCADE,
 latitude float8 NOT NULL CHECK(latitude BETWEEN -90 AND 90),
 longitude float8 NOT NULL CHECK(longitude BETWEEN -180 AND 180),
 accuracy_m float8 NOT NULL CHECK(accuracy_m BETWEEN 0 AND 1000),
 updated_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL
);
CREATE INDEX driver_presence_point ON provider_driver_presence USING gist (point(longitude,latitude));
CREATE INDEX driver_presence_expiry ON provider_driver_presence(expires_at);
-- Latest location only. No driver travel history is stored.
CREATE OR REPLACE FUNCTION purge_expired_driver_presence(batch_size integer DEFAULT 1000) RETURNS integer LANGUAGE plpgsql AS $$
DECLARE removed integer;
BEGIN
 WITH expired AS (SELECT provider_id FROM provider_driver_presence WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT LEAST(GREATEST(batch_size,1),1000) FOR UPDATE SKIP LOCKED)
 DELETE FROM provider_driver_presence d USING expired e WHERE d.provider_id=e.provider_id;
 GET DIAGNOSTICS removed=ROW_COUNT;
 RETURN removed;
END;$$;
