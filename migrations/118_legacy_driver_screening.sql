-- Evidence-based transition, independent of Jobs. Empty by default: no automatic
-- grandfathering, reapproval, subscription change or document-status updates.
CREATE TABLE provider_driver_legacy_screening (
 provider_id uuid PRIMARY KEY REFERENCES provider_organizations(id) ON DELETE RESTRICT,
 source_listing_id uuid NOT NULL REFERENCES provider_listings(id) ON DELETE RESTRICT,
 source_approved_at timestamptz NOT NULL,
 rollout_at timestamptz NOT NULL,
 backup_taken_at timestamptz NOT NULL,
 backup_sha256 text NOT NULL CHECK(backup_sha256 ~ '^[a-f0-9]{64}$'),
 evidence_reference text NOT NULL CHECK(char_length(evidence_reference) BETWEEN 10 AND 1000),
 enrolled_by uuid NOT NULL REFERENCES admins(id) ON DELETE RESTRICT,
 enrolled_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 revoked_at timestamptz,
 CHECK(source_approved_at<=backup_taken_at AND backup_taken_at<rollout_at)
);
COMMENT ON TABLE provider_driver_legacy_screening IS
 'Explicit reviewed evidence of transport eligibility before migration115. Account age alone never qualifies. Enrollment does not restore listings.';
CREATE FUNCTION validate_driver_legacy_screening() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cutoff timestamptz;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF to_jsonb(NEW)-'revoked_at' IS DISTINCT FROM to_jsonb(OLD)-'revoked_at' THEN
   RAISE EXCEPTION 'Legacy screening evidence is immutable' USING ERRCODE='23514';
  END IF;
  IF OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
   RAISE EXCEPTION 'Revoked legacy screening cannot be reinstated' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 SELECT applied_at INTO cutoff FROM schema_migrations WHERE name='115_driver_safety_identity.sql';
 IF cutoff IS NULL OR NEW.rollout_at IS DISTINCT FROM cutoff THEN
  RAISE EXCEPTION 'Use the confirmed original production driver rollout cutoff' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM admins WHERE id=NEW.enrolled_by AND is_active AND role='super_admin') THEN
  RAISE EXCEPTION 'A current super admin must own the reviewed transition evidence' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM provider_listings l JOIN provider_organizations p ON p.id=l.provider_id
  WHERE l.id=NEW.source_listing_id AND p.id=NEW.provider_id AND p.is_active AND p.verification_status='approved'
  AND l.status IN('approved','suspended') AND (l.moderated_at IS NULL OR l.moderated_at<cutoff)
  AND ((l.category||' '||l.title) ~* '(^|[^a-z])(drivers?|chauffeurs?|taxi|riders?|couriers?|dispatch)([^a-z]|$)' OR p.is_driver)) THEN
  RAISE EXCEPTION 'Listing ownership, prior transport eligibility or moderation needs manual review' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER validate_driver_legacy_screening BEFORE INSERT OR UPDATE ON provider_driver_legacy_screening
 FOR EACH ROW EXECUTE FUNCTION validate_driver_legacy_screening();
CREATE FUNCTION driver_has_legacy_screening(provider uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM provider_driver_legacy_screening e JOIN schema_migrations m
 ON m.name='115_driver_safety_identity.sql' AND m.applied_at=e.rollout_at
 WHERE e.provider_id=provider AND e.revoked_at IS NULL AND e.source_approved_at<e.rollout_at);
$$;
CREATE OR REPLACE FUNCTION driver_required_documents(provider uuid)
RETURNS TABLE(document_type text,requires_expiry boolean) LANGUAGE sql STABLE AS $$
 SELECT kind,CASE WHEN kind='drivers_license' THEN NOT legacy ELSE false END
 FROM (SELECT driver_has_legacy_screening(provider) AS legacy) policy
 CROSS JOIN LATERAL unnest(CASE WHEN legacy THEN ARRAY['drivers_license','driver_vehicle','driver_plate']
 ELSE ARRAY['drivers_license','driver_face','driver_vehicle','driver_plate','vehicle_registration'] END) kind;
$$;
-- Do not apply newly introduced expiry metadata checks to the reviewed cohort.
-- A rejected/pending/missing required document still fails the ORIGINAL checks.
-- This preserves the old screening policy, not a claim of current licence validity.
CREATE OR REPLACE FUNCTION driver_documents_approved(provider uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS (
 SELECT 1 FROM driver_required_documents(provider) r LEFT JOIN LATERAL (
 SELECT d.status,d.expires_on FROM provider_verification_documents d
 WHERE d.provider_id=provider AND d.document_type=r.document_type ORDER BY d.created_at DESC,d.id DESC LIMIT 1
 ) latest ON true WHERE latest.status IS DISTINCT FROM 'approved'
 OR (NOT driver_has_legacy_screening(provider) AND
 ((r.requires_expiry AND latest.expires_on IS NULL) OR latest.expires_on<CURRENT_DATE)));
$$;
CREATE FUNCTION revoke_driver_legacy_screening() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.revoked_at IS NOT NULL AND OLD.revoked_at IS NULL THEN
  UPDATE provider_listings SET status='suspended' WHERE provider_id=NEW.provider_id AND status='approved'
   AND NOT driver_documents_approved(NEW.provider_id)
   AND ((category||' '||title) ~* '(^|[^a-z])(drivers?|chauffeurs?|taxi|riders?|couriers?|dispatch)([^a-z]|$)'
    OR EXISTS(SELECT 1 FROM provider_organizations p WHERE p.id=NEW.provider_id AND p.is_driver));
  DELETE FROM provider_driver_presence WHERE provider_id=NEW.provider_id AND NOT driver_documents_approved(NEW.provider_id);
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER revoke_driver_legacy_screening AFTER UPDATE OF revoked_at ON provider_driver_legacy_screening
 FOR EACH ROW EXECUTE FUNCTION revoke_driver_legacy_screening();
-- Restoration is intentionally a separate reviewed operation: the backup must
-- show approved/eligible status immediately before rollout, and live moderation
-- must show that the checklist change was the sole cause of the pause.
