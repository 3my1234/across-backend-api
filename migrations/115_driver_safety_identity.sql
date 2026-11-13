-- Additive schema change. Apply before deploying the updated driver endpoints.
ALTER TABLE provider_verification_documents ADD COLUMN expires_on DATE;
CREATE INDEX idx_provider_document_latest ON provider_verification_documents(provider_id,document_type,created_at DESC,id DESC);

-- ATL's motor-vehicle screening policy; not a universal statement of local law.
CREATE FUNCTION driver_required_documents(provider UUID)
RETURNS TABLE(document_type TEXT,requires_expiry BOOLEAN) LANGUAGE sql STABLE AS $$
 SELECT * FROM (VALUES ('driver_face',false),('driver_vehicle',false),('driver_plate',false),
 ('drivers_license',true),('vehicle_registration',false),('vehicle_insurance',true),('roadworthiness',true)) core
 UNION ALL
 SELECT permit,false FROM provider_organizations p CROSS JOIN unnest(ARRAY['driver_lasrra','driver_lasdri']) permit
 WHERE p.id=provider AND upper(p.country_code)='NG' AND lower(trim(p.state)) IN ('lagos','lagos state');
$$;

CREATE OR REPLACE FUNCTION driver_documents_approved(provider UUID) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS (
 SELECT 1 FROM driver_required_documents(provider) r LEFT JOIN LATERAL (
 SELECT d.status,d.expires_on FROM provider_verification_documents d
 WHERE d.provider_id=provider AND d.document_type=r.document_type ORDER BY d.created_at DESC,d.id DESC LIMIT 1
 ) latest ON true WHERE latest.status IS DISTINCT FROM 'approved'
 OR (r.requires_expiry AND latest.expires_on IS NULL)
 OR latest.expires_on<CURRENT_DATE);
$$;

CREATE OR REPLACE FUNCTION enforce_driver_documents() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE provider uuid; required boolean;
BEGIN
 IF TG_TABLE_NAME='provider_organizations' THEN
  provider:=NEW.id;
  required:=NEW.is_driver OR EXISTS(SELECT 1 FROM provider_listings WHERE provider_id=provider AND (category||' '||title) ~* '(^|[^a-z])(drivers?|chauffeurs?|taxi|riders?|couriers?|dispatch)([^a-z]|$)');
  IF NEW.verification_status<>'approved' THEN RETURN NEW;END IF;
 ELSE
  provider:=NEW.provider_id;
  required:=(NEW.category||' '||NEW.title) ~* '(^|[^a-z])(drivers?|chauffeurs?|taxi|riders?|couriers?|dispatch)([^a-z]|$)'
  OR EXISTS(SELECT 1 FROM provider_organizations WHERE id=provider AND is_driver);
  IF NEW.status<>'approved' THEN RETURN NEW;END IF;
 END IF;
 IF required AND NOT driver_documents_approved(provider) THEN
  RAISE EXCEPTION 'Driver verification requires current approved face, vehicle, plate, licence, registration, insurance, roadworthiness and applicable local permits.' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;$$;

CREATE OR REPLACE FUNCTION revoke_driver_listing_access() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE provider uuid;
BEGIN
 IF TG_OP='DELETE' THEN provider:=OLD.provider_id; ELSE provider:=NEW.provider_id; END IF;
 IF NOT driver_documents_approved(provider) THEN
  UPDATE provider_listings SET status='suspended' WHERE provider_id=provider AND status='approved'
  AND ((category||' '||title) ~* '(^|[^a-z])(drivers?|chauffeurs?|taxi|riders?|couriers?|dispatch)([^a-z]|$)'
  OR EXISTS(SELECT 1 FROM provider_organizations p WHERE p.id=provider AND p.is_driver));
  DELETE FROM provider_driver_presence WHERE provider_id=provider;
 END IF;
 RETURN NULL;
END;$$;
DROP TRIGGER driver_document_revoked ON provider_verification_documents;
CREATE TRIGGER driver_document_revoked AFTER INSERT OR UPDATE OR DELETE ON provider_verification_documents
FOR EACH ROW EXECUTE FUNCTION revoke_driver_listing_access();

-- Existing transport listings need the same reviewed evidence before discovery.
-- No account, subscription, document or customer record is deleted.
UPDATE provider_listings SET status='suspended' WHERE status='approved'
AND ((category||' '||title) ~* '(^|[^a-z])(drivers?|chauffeurs?|taxi|riders?|couriers?|dispatch)([^a-z]|$)'
OR EXISTS(SELECT 1 FROM provider_organizations p WHERE p.id=provider_id AND p.is_driver))
AND NOT driver_documents_approved(provider_id);
DELETE FROM provider_driver_presence WHERE NOT driver_documents_approved(provider_id);
