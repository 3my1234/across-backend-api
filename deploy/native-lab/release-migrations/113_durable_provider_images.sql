-- Opt-in processing for NEW public provider images; no existing media is deleted.
ALTER TABLE provider_video_launch_policy ADD COLUMN image_quality integer NOT NULL DEFAULT 80 CHECK(image_quality BETWEEN 50 AND 90),
 ADD COLUMN image_processing_required boolean NOT NULL DEFAULT false,
 ADD COLUMN image_daily_limit integer NOT NULL DEFAULT 40 CHECK(image_daily_limit BETWEEN 1 AND 200),
 ADD COLUMN image_pending_limit integer NOT NULL DEFAULT 4 CHECK(image_pending_limit BETWEEN 1 AND 10),
 ADD COLUMN image_stored_limit_bytes bigint NOT NULL DEFAULT 262144000 CHECK(image_stored_limit_bytes>=16777216),
 ADD COLUMN image_queue_limit integer NOT NULL DEFAULT 20 CHECK(image_queue_limit BETWEEN 1 AND 100);
CREATE TABLE provider_image_uploads (
 key text PRIMARY KEY,provider_id uuid NOT NULL REFERENCES provider_organizations(id),asset_id uuid NOT NULL UNIQUE,
 mime_type text NOT NULL CHECK(mime_type IN('image/jpeg','image/png','image/webp')),
 scope text NOT NULL CHECK(scope IN('listing','logo','banner')),
 size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 10485760),
 processing_mode text NOT NULL DEFAULT 'background' CHECK(processing_mode='background'),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','queued','processing','complete','failed')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 3),claim_token uuid,
 created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL DEFAULT now()+interval '20 minutes',
 available_at timestamptz NOT NULL DEFAULT now(),lease_until timestamptz,error_code text,
 url text,thumbnail_url text,fallback_url text,
 CHECK(state<>'complete' OR (url IS NOT NULL AND thumbnail_url IS NOT NULL AND fallback_url IS NOT NULL))
);
CREATE INDEX provider_image_upload_daily ON provider_image_uploads(provider_id,created_at);
CREATE INDEX provider_image_processing_due ON provider_image_uploads(available_at,created_at) WHERE state IN('queued','processing');
CREATE TABLE provider_image_assets(
 url text PRIMARY KEY,provider_id uuid NOT NULL REFERENCES provider_organizations(id),thumbnail_url text NOT NULL,
 fallback_url text NOT NULL,width integer NOT NULL CHECK(width BETWEEN 1 AND 1600),height integer NOT NULL CHECK(height BETWEEN 1 AND 1600),
 size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 16777216),created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX provider_image_assets_owner ON provider_image_assets(provider_id);
CREATE FUNCTION enforce_processed_provider_images() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE previous jsonb;current_row jsonb;images jsonb;old_images jsonb;item jsonb;required boolean;
BEGIN
 SELECT image_processing_required INTO required FROM provider_video_launch_policy WHERE singleton;
 IF NOT required THEN RETURN NEW;END IF;
 current_row:=to_jsonb(NEW);previous:=CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
 images:=COALESCE(current_row->'image_urls',current_row->'media_urls','[]'::jsonb);
 old_images:=COALESCE(previous->'image_urls',previous->'media_urls','[]'::jsonb);
 FOR item IN SELECT value FROM jsonb_array_elements(images) WHERE NOT(old_images @> jsonb_build_array(value)) LOOP
  IF NOT EXISTS(SELECT 1 FROM provider_image_assets WHERE url=item#>>'{}' AND provider_id=NEW.provider_id) THEN
   RAISE EXCEPTION 'Your plan requires images processed through your provider upload.' USING ERRCODE='23514';END IF;
 END LOOP;
 RETURN NEW;
END;$$;
CREATE TRIGGER provider_product_processed_images BEFORE INSERT OR UPDATE ON products FOR EACH ROW EXECUTE FUNCTION enforce_processed_provider_images();
CREATE TRIGGER provider_service_processed_images BEFORE INSERT OR UPDATE ON provider_listings FOR EACH ROW EXECUTE FUNCTION enforce_processed_provider_images();

CREATE FUNCTION enforce_processed_provider_branding() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE item text;
BEGIN
 IF NOT (SELECT image_processing_required FROM provider_video_launch_policy WHERE singleton) THEN RETURN NEW;END IF;
 IF NEW.logo_url IS DISTINCT FROM OLD.logo_url AND COALESCE(NEW.logo_url,'')<>'' AND NOT EXISTS(SELECT 1 FROM provider_image_assets WHERE provider_id=NEW.id AND url=NEW.logo_url) THEN RAISE EXCEPTION 'Your plan requires a processed profile image from your provider account.' USING ERRCODE='23514';END IF;
 IF NEW.banner_url IS DISTINCT FROM OLD.banner_url AND COALESCE(NEW.banner_url,'')<>'' AND NOT EXISTS(SELECT 1 FROM provider_image_assets WHERE provider_id=NEW.id AND url=NEW.banner_url) THEN RAISE EXCEPTION 'Your plan requires a processed banner from your provider account.' USING ERRCODE='23514';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER provider_brand_processed_images BEFORE UPDATE OF logo_url,banner_url ON provider_organizations FOR EACH ROW EXECUTE FUNCTION enforce_processed_provider_branding();
