-- Keep the last approved content, rather than just the immediately prior edit.
ALTER TABLE provider_listings ADD COLUMN approved_snapshot jsonb;
ALTER TABLE provider_listings ADD COLUMN content_revision bigint NOT NULL DEFAULT 0;

CREATE FUNCTION listing_review_content(document jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
 SELECT COALESCE(jsonb_object_agg(key,CASE WHEN key='attributes' AND value='null'::jsonb THEN '{}'::jsonb ELSE value END),'{}'::jsonb) FROM jsonb_each(document)
 WHERE key=ANY(ARRAY['listing_type','title','description','category','contact_email','contact_phone',
 'address_line','city','state','country_code','price','currency_code','pricing_unit','capacity',
 'media_urls','attributes','latitude','longitude','service_radius_km','is_mobile_service','is_available_now']);
$$;

UPDATE provider_listings l SET approved_snapshot=listing_review_content(to_jsonb(l)) WHERE status='approved';

CREATE FUNCTION maintain_listing_review_baseline() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE previous jsonb; current_content jsonb; changed boolean;
BEGIN
 previous:=listing_review_content(to_jsonb(OLD));
 current_content:=listing_review_content(to_jsonb(NEW));
 changed:=previous IS DISTINCT FROM current_content;
 NEW.content_revision:=OLD.content_revision+CASE WHEN changed THEN 1 ELSE 0 END;
 NEW.approved_snapshot:=OLD.approved_snapshot;
 IF OLD.status='approved' AND NEW.approved_snapshot IS NULL THEN NEW.approved_snapshot:=previous; END IF;
 IF OLD.status='approved' AND changed THEN NEW.status:='pending'; END IF;
 -- A provider saving identical content should not withdraw an approved listing.
 IF OLD.status='approved' AND NOT changed AND NEW.status='pending' THEN
   NEW.status:='approved'; NEW.moderation_notes:=OLD.moderation_notes;
 END IF;
 IF NEW.status='approved' THEN NEW.approved_snapshot:=current_content; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER provider_listing_review_baseline BEFORE UPDATE ON provider_listings
FOR EACH ROW EXECUTE FUNCTION maintain_listing_review_baseline();
