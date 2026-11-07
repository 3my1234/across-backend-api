-- Renewal gateway work must not hold a pooled database connection. A durable,
-- fenced operation lease replaces the long-lived session advisory lock. The
-- existing short transaction advisory lock still arbitrates checkout admission.
ALTER TABLE provider_renewal_preferences
 ADD COLUMN operation_token uuid,
 ADD COLUMN operation_expires_at timestamptz,
 ADD CONSTRAINT provider_renewal_operation_pair CHECK
 ((operation_token IS NULL) = (operation_expires_at IS NULL));

-- Old API replicas must not bypass a new replica's in-flight lease during a
-- rolling release. They update preferences before calling the gateway, so reject
-- that update unless this short transaction presents the matching fence token.
CREATE FUNCTION protect_provider_renewal_operation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.operation_token IS NOT NULL AND OLD.operation_expires_at>clock_timestamp()
 AND current_setting('app.renewal_operation_token',true) IS DISTINCT FROM OLD.operation_token::text THEN
  RAISE EXCEPTION 'Renewal change is already in progress' USING ERRCODE='P0503';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER provider_renewal_operation_fence BEFORE UPDATE OR DELETE ON provider_renewal_preferences
 FOR EACH ROW EXECUTE FUNCTION protect_provider_renewal_operation();
