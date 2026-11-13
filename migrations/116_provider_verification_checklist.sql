-- Revised ATL upload/review checklist. This changes platform screening only.
-- Preserve all existing documents, approvals, listings and subscription records.
-- A current approved driver licence also fulfils identification; no duplicate ID.
CREATE OR REPLACE FUNCTION driver_required_documents(provider UUID)
RETURNS TABLE(document_type TEXT,requires_expiry BOOLEAN) LANGUAGE sql STABLE AS $$
 SELECT * FROM (VALUES ('drivers_license',true),('driver_face',false),
 ('driver_vehicle',false),('driver_plate',false),('vehicle_registration',false)) core;
$$;

-- Existing approval/revocation/presence checks already call this function.
-- Do not automatically reapprove previously suspended or unreviewed listings.
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
  RAISE EXCEPTION 'Driver verification requires current approved face, vehicle, plate, driver licence (also accepted as ID) and vehicle registration.' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;$$;

