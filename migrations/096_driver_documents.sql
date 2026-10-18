ALTER TABLE provider_organizations ADD COLUMN is_driver BOOLEAN NOT NULL DEFAULT false;
CREATE FUNCTION driver_documents_approved(provider UUID) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT count(DISTINCT document_type)=3 FROM provider_verification_documents
 WHERE provider_id=provider AND status='approved' AND document_type IN('drivers_license','driver_vehicle','driver_plate');
$$;
CREATE FUNCTION enforce_driver_documents() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE provider uuid; required boolean;
BEGIN
 IF TG_TABLE_NAME='provider_organizations' THEN
  provider:=NEW.id;
  required:=NEW.is_driver OR EXISTS(SELECT 1 FROM provider_listings WHERE provider_id=provider AND (category||' '||title) ~* '(^|[^a-z])(driver|chauffeur|taxi|uber|bolt)([^a-z]|$)');
  IF NEW.verification_status<>'approved' THEN RETURN NEW;END IF;
 ELSE
  provider:=NEW.provider_id;
  required:=(NEW.category||' '||NEW.title) ~* '(^|[^a-z])(driver|chauffeur|taxi|uber|bolt)([^a-z]|$)';
  IF NEW.status<>'approved' THEN RETURN NEW;END IF;
 END IF;
 IF required AND NOT driver_documents_approved(provider) THEN RAISE EXCEPTION 'Driver verification requires approved clear licence, vehicle and plate-number photos before approval.' USING ERRCODE='23514';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER provider_driver_documents BEFORE INSERT OR UPDATE OF verification_status,is_driver ON provider_organizations FOR EACH ROW EXECUTE FUNCTION enforce_driver_documents();
CREATE TRIGGER listing_driver_documents BEFORE INSERT OR UPDATE OF status,category,title ON provider_listings FOR EACH ROW EXECUTE FUNCTION enforce_driver_documents();

-- Existing driver listings return to review until the new evidence is approved.
UPDATE provider_listings SET status='suspended' WHERE status='approved' AND (category||' '||title) ~* '(^|[^a-z])(driver|chauffeur|taxi|uber|bolt)([^a-z]|$)' AND NOT driver_documents_approved(provider_id);
CREATE FUNCTION revoke_driver_listing_access() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE provider uuid;
BEGIN
 provider:=OLD.provider_id;
 IF OLD.document_type IN('drivers_license','driver_vehicle','driver_plate') AND NOT driver_documents_approved(provider) THEN
  UPDATE provider_listings SET status='suspended' WHERE provider_id=provider AND status='approved' AND (category||' '||title) ~* '(^|[^a-z])(driver|chauffeur|taxi|uber|bolt)([^a-z]|$)';
  DELETE FROM provider_driver_presence WHERE provider_id=provider;
 END IF;
 RETURN NULL;
END;$$;
CREATE TRIGGER driver_document_revoked AFTER UPDATE OF status OR DELETE ON provider_verification_documents FOR EACH ROW EXECUTE FUNCTION revoke_driver_listing_access();
