-- Explicit static pins: private conversation membership controls their visibility.
ALTER TABLE chat_messages ADD COLUMN IF NOT EXISTS location jsonb;
ALTER TABLE provider_conversation_messages ADD COLUMN IF NOT EXISTS location jsonb;
CREATE OR REPLACE FUNCTION valid_chat_location(value jsonb) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
 SELECT value IS NULL OR (jsonb_typeof(value)='object'
 AND jsonb_typeof(value->'latitude')='number' AND jsonb_typeof(value->'longitude')='number'
 AND (value->>'latitude')::float8 BETWEEN -90 AND 90
 AND (value->>'longitude')::float8 BETWEEN -180 AND 180
 AND (NOT(value ? 'accuracy_m') OR (jsonb_typeof(value->'accuracy_m')='number' AND (value->>'accuracy_m')::float8 BETWEEN 0 AND 5000))
 AND value - ARRAY['latitude','longitude','accuracy_m'] = '{}'::jsonb);
$$;
ALTER TABLE chat_messages ADD CONSTRAINT chat_location_valid CHECK(valid_chat_location(location) IS TRUE);
ALTER TABLE provider_conversation_messages ADD CONSTRAINT provider_chat_location_valid CHECK(valid_chat_location(location) IS TRUE);
