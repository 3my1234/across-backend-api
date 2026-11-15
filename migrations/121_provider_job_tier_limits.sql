-- Independent active Jobs: Free/Bronze/Silver=1, Diamond=5, Gold=10.
-- No monthly publication counter; deadline passage immediately releases capacity.
-- Existing jobs, applications and product/service places are preserved.
CREATE FUNCTION provider_active_job_limit(provider uuid) RETURNS integer LANGUAGE sql STABLE AS $$
 SELECT CASE COALESCE((SELECT badge_tier FROM provider_public_identity WHERE provider_id=provider),'') WHEN 'diamond' THEN 5 WHEN 'gold' THEN 10 ELSE 1 END;
$$;
CREATE OR REPLACE FUNCTION provider_job_slot_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE used integer; verified boolean; retained boolean := false;
BEGIN
 IF TG_OP='UPDATE' AND NEW.provider_id<>OLD.provider_id THEN RAISE EXCEPTION 'Job owner cannot change' USING ERRCODE='23514';END IF;
 -- Serialize all publishes for this provider, including concurrent requests on different jobs.
 PERFORM 1 FROM provider_organizations WHERE id=NEW.provider_id FOR UPDATE;
 IF NEW.status='published' AND NEW.deadline>clock_timestamp() THEN
  SELECT is_active AND verification_status='approved' INTO verified FROM provider_organizations WHERE id=NEW.provider_id;
  IF verified IS DISTINCT FROM true THEN RAISE EXCEPTION 'Provider approval is required to publish jobs' USING ERRCODE='23514';END IF;
  SELECT count(*) INTO used FROM provider_jobs WHERE provider_id=NEW.provider_id AND id<>NEW.id AND status='published' AND deadline>clock_timestamp();
  -- Keep historical overages editable until their existing deadline, without granting extensions
  -- or additional publications. No migration rewrites or removes valid existing jobs.
  IF TG_OP='UPDATE' THEN retained := OLD.status='published' AND OLD.deadline>clock_timestamp() AND NEW.deadline<=OLD.deadline;END IF;
  IF used>=provider_active_job_limit(NEW.provider_id) AND NOT retained THEN
   RAISE EXCEPTION 'Your active job allowance is full. Close or fill an active job, or wait for its deadline, before publishing another. Product and service places are separate.' USING ERRCODE='23514';
  END IF;
  NEW.published_at:=COALESCE(NEW.published_at,clock_timestamp());
  NEW.plan_slot:=1; -- Dedicated Jobs namespace; never competes with a product/service slot.
 ELSE NEW.plan_slot:=NULL;
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION provider_current_benefits(provider uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT provider_current_benefits_before_jobs(provider)||jsonb_build_object('active_job_limit',provider_active_job_limit(provider),'active_jobs_used',
 (SELECT count(*) FROM provider_jobs WHERE provider_id=provider AND status='published' AND deadline>clock_timestamp()));
$$;

