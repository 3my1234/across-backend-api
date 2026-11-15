-- Additive Jobs release. Rehearse and apply before deploying Jobs code.
CREATE TABLE provider_jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), provider_id uuid NOT NULL REFERENCES provider_organizations(id) ON DELETE RESTRICT,
 title text NOT NULL DEFAULT '',city text NOT NULL DEFAULT '',state text NOT NULL DEFAULT '',country_code text NOT NULL DEFAULT 'NG',
 work_type text NOT NULL DEFAULT '',work_duration text NOT NULL DEFAULT '',description text NOT NULL DEFAULT '',requirements text NOT NULL DEFAULT '',
 pay_min numeric(14,2),pay_max numeric(14,2),currency_code text NOT NULL DEFAULT 'NGN',pay_period text NOT NULL DEFAULT 'month',
 application_method text NOT NULL DEFAULT 'chat' CHECK(application_method IN('external','email','chat')),
 application_url text NOT NULL DEFAULT '',application_email text NOT NULL DEFAULT '',application_instructions text NOT NULL DEFAULT '',
 deadline timestamptz,status text NOT NULL DEFAULT 'draft' CHECK(status IN('draft','published','closed','filled','removed')),
 published_at timestamptz,plan_slot integer,revision bigint NOT NULL DEFAULT 1,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(pay_min IS NULL OR pay_min>=0),CHECK(pay_max IS NULL OR(pay_min IS NOT NULL AND pay_max>=pay_min)),
 CHECK(status<>'published' OR(deadline IS NOT NULL AND title<>'' AND city<>'' AND work_type<>'' AND description<>'' AND requirements<>''))
);
CREATE INDEX provider_jobs_owner ON provider_jobs(provider_id,created_at DESC,id DESC);
CREATE INDEX provider_jobs_browse ON provider_jobs(status,deadline,created_at DESC,id DESC);
CREATE INDEX provider_jobs_slot ON provider_jobs(provider_id,plan_slot) WHERE status='published';
CREATE TABLE provider_job_reports (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),job_id uuid NOT NULL REFERENCES provider_jobs(id),user_id uuid NOT NULL REFERENCES users(id),
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 3 AND 1000),status text NOT NULL DEFAULT 'open' CHECK(status IN('open','dismissed')),
 reviewed_by uuid REFERENCES admins(id),review_notes text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),reviewed_at timestamptz
);
CREATE UNIQUE INDEX provider_job_report_open ON provider_job_reports(job_id,user_id) WHERE status='open';
CREATE INDEX provider_job_reports_queue ON provider_job_reports(job_id,status,created_at DESC);
CREATE TABLE provider_job_events (
 id uuid PRIMARY KEY,job_id uuid NOT NULL REFERENCES provider_jobs(id),user_id uuid NOT NULL REFERENCES users(id),
 kind text NOT NULL CHECK(kind IN('external_click','email_click')),created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX provider_job_events_job ON provider_job_events(job_id,created_at);
CREATE TABLE provider_job_moderation (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),job_id uuid NOT NULL REFERENCES provider_jobs(id),admin_id uuid NOT NULL REFERENCES admins(id),
 action text NOT NULL CHECK(action IN('remove','dismiss_report')),reason text NOT NULL,report_id uuid REFERENCES provider_job_reports(id),created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE provider_conversations ADD COLUMN job_id uuid REFERENCES provider_jobs(id) ON DELETE RESTRICT;
ALTER TABLE provider_conversations DROP CONSTRAINT conversation_subject_check;
ALTER TABLE provider_conversations ADD CONSTRAINT conversation_subject_check CHECK(num_nonnulls(listing_id,product_id,job_id)=1);
CREATE UNIQUE INDEX provider_conversation_job_buyer ON provider_conversations(job_id,user_id) WHERE job_id IS NOT NULL;
CREATE TABLE provider_job_applications (
 id uuid PRIMARY KEY,job_id uuid NOT NULL REFERENCES provider_jobs(id),user_id uuid NOT NULL REFERENCES users(id),
 conversation_id uuid NOT NULL REFERENCES provider_conversations(id),message_id uuid NOT NULL REFERENCES provider_conversation_messages(id),
 created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(job_id,user_id)
);
CREATE OR REPLACE FUNCTION provider_message_context() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 SELECT jsonb_build_object('listing_id',c.listing_id,'product_id',c.product_id,'job_id',c.job_id,'title',COALESCE(l.title,p.title,j.title,'')) INTO NEW.context
 FROM provider_conversations c LEFT JOIN provider_listings l ON l.id=c.listing_id LEFT JOIN products p ON p.id=c.product_id LEFT JOIN provider_jobs j ON j.id=c.job_id WHERE c.id=NEW.conversation_id;
 RETURN NEW;
END $$;
CREATE FUNCTION provider_job_slot_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cap integer; used integer; assigned integer; verified boolean;
BEGIN
 IF TG_OP='UPDATE' AND NEW.provider_id<>OLD.provider_id THEN RAISE EXCEPTION 'Job owner cannot change' USING ERRCODE='23514';END IF;
 PERFORM 1 FROM provider_organizations WHERE id=NEW.provider_id FOR UPDATE;
 IF NEW.status='published' AND NEW.deadline>clock_timestamp() THEN
  NEW.published_at:=COALESCE(NEW.published_at,clock_timestamp());
  SELECT is_active AND verification_status='approved' INTO verified FROM provider_organizations WHERE id=NEW.provider_id;
  IF verified IS DISTINCT FROM true THEN RAISE EXCEPTION 'Provider approval is required to publish jobs' USING ERRCODE='23514';END IF;
  cap:=(provider_current_benefits(NEW.provider_id)->>'post_limit')::int;
  SELECT (SELECT count(*) FROM products WHERE provider_id=NEW.provider_id AND moderation_status<>'archived')+
   (SELECT count(*) FROM provider_listings WHERE provider_id=NEW.provider_id AND status<>'archived')+
   (SELECT count(*) FROM provider_jobs WHERE provider_id=NEW.provider_id AND id<>NEW.id AND status='published' AND deadline>clock_timestamp()) INTO used;
  IF cap IS NULL OR used>=cap THEN RAISE EXCEPTION 'Your shared listing allowance is full. Close an active job, archive a product/service, or upgrade before publishing.' USING ERRCODE='23514';END IF;
  SELECT n INTO assigned FROM generate_series(1,cap) n
   WHERE NOT EXISTS(SELECT 1 FROM products WHERE provider_id=NEW.provider_id AND moderation_status<>'archived' AND plan_slot=n)
   AND NOT EXISTS(SELECT 1 FROM provider_listings WHERE provider_id=NEW.provider_id AND status<>'archived' AND plan_slot=n)
   AND NOT EXISTS(SELECT 1 FROM provider_jobs WHERE provider_id=NEW.provider_id AND id<>NEW.id AND status='published' AND deadline>clock_timestamp() AND plan_slot=n)
   ORDER BY n LIMIT 1;
  IF assigned IS NULL THEN RAISE EXCEPTION 'Your shared listing allowance is full' USING ERRCODE='23514';END IF;
  NEW.plan_slot:=assigned;
 ELSE NEW.plan_slot:=NULL;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER provider_job_slot BEFORE INSERT OR UPDATE ON provider_jobs FOR EACH ROW EXECUTE FUNCTION provider_job_slot_guard();

-- Extend shared capacity only; product/service draft/media rules are unchanged.
CREATE OR REPLACE FUNCTION enforce_provider_listing_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cap integer; used integer; provider uuid; tier text; max_images integer; max_videos integer; images jsonb; videos jsonb; item jsonb; previous jsonb; row_data jsonb;
BEGIN
 provider:=NEW.provider_id;IF provider IS NULL THEN RETURN NEW;END IF;
 IF TG_OP='UPDATE' AND NEW.provider_id IS DISTINCT FROM OLD.provider_id THEN RAISE EXCEPTION 'Your plan listing owner cannot be changed.' USING ERRCODE='23514';END IF;
 PERFORM 1 FROM provider_organizations WHERE id=provider FOR UPDATE;
 SELECT badge_tier INTO tier FROM provider_public_identity WHERE provider_id=provider;
 cap:=(provider_current_benefits(provider)->>'post_limit')::int;
 IF cap IS NULL THEN RAISE EXCEPTION 'Provider profile is required';END IF;
 row_data:=to_jsonb(NEW);previous:=CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
 IF TG_OP='INSERT' OR (COALESCE(previous->>'status',previous->>'moderation_status')='archived' AND COALESCE(row_data->>'status',row_data->>'moderation_status')<>'archived') THEN
  SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status<>'archived' AND id<>NEW.id)+(SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status<>'archived' AND id<>NEW.id)+(SELECT count(*) FROM provider_jobs WHERE provider_id=provider AND status='published' AND deadline>clock_timestamp()) INTO used;
  IF used>=cap THEN RAISE EXCEPTION 'Your plan post limit has been reached. Products, services and active published jobs share one limit. Archive a post or upgrade.' USING ERRCODE='23514';END IF;
 END IF;
 max_images:=(provider_tier_benefits(tier)->>'max_images')::int;
 max_videos:=(provider_tier_benefits(tier)->>'max_videos')::int;
 images:=COALESCE(row_data->'image_urls',row_data->'media_urls','[]'::jsonb);
 videos:=COALESCE(row_data#>'{attributes,video_urls}',row_data#>'{factory_details,video_urls}','[]'::jsonb);
 -- Preserve existing records. New media or resubmission must meet today's plan.
 IF TG_OP='INSERT' OR row_data->'image_urls' IS DISTINCT FROM previous->'image_urls' OR row_data->'media_urls' IS DISTINCT FROM previous->'media_urls' OR row_data#>'{attributes,video_urls}' IS DISTINCT FROM previous#>'{attributes,video_urls}' OR row_data#>'{factory_details,video_urls}' IS DISTINCT FROM previous#>'{factory_details,video_urls}' THEN
  IF jsonb_typeof(images)<>'array' OR (jsonb_array_length(images)>max_images AND (TG_OP='INSERT' OR NOT(images <@ COALESCE(previous->'image_urls',previous->'media_urls','[]'::jsonb)) OR jsonb_array_length(images)>jsonb_array_length(COALESCE(previous->'image_urls',previous->'media_urls','[]'::jsonb)))) THEN RAISE EXCEPTION 'Your plan allows at most % image(s) per post. Upgrade for more images.',max_images USING ERRCODE='23514';END IF;
  IF jsonb_typeof(videos)<>'array' OR (jsonb_array_length(videos)>max_videos AND (TG_OP='INSERT' OR NOT(videos <@ COALESCE(previous#>'{attributes,video_urls}',previous#>'{factory_details,video_urls}','[]'::jsonb)) OR jsonb_array_length(videos)>jsonb_array_length(COALESCE(previous#>'{attributes,video_urls}',previous#>'{factory_details,video_urls}','[]'::jsonb)))) THEN RAISE EXCEPTION 'Your plan allows at most % video(s) per post. Upgrade to Gold or Diamond for videos.',max_videos USING ERRCODE='23514';END IF;
  FOR item IN SELECT value FROM jsonb_array_elements(videos) WHERE NOT(COALESCE(previous#>'{attributes,video_urls}','[]'::jsonb) @> jsonb_build_array(value) OR COALESCE(previous#>'{factory_details,video_urls}','[]'::jsonb) @> jsonb_build_array(value)) OR TG_OP='INSERT' LOOP
   IF NOT EXISTS(SELECT 1 FROM provider_post_video_assets a WHERE a.provider_id=provider AND a.url=item#>>'{}' AND a.duration_seconds<=60) THEN RAISE EXCEPTION 'Your plan requires verified videos of no more than 60 seconds each. Upload the video again.' USING ERRCODE='23514';END IF;
  END LOOP;
 END IF;
 IF TG_OP='UPDATE' AND previous->>'published_at' IS NULL AND COALESCE(row_data->>'status',row_data->>'moderation_status') IN('pending','approved') AND COALESCE(row_data->>'status',row_data->>'moderation_status') IS DISTINCT FROM COALESCE(previous->>'status',previous->>'moderation_status') THEN
  SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status<>'archived' AND id<>NEW.id)+(SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status<>'archived' AND id<>NEW.id)+1+(SELECT count(*) FROM provider_jobs WHERE provider_id=provider AND status='published' AND deadline>clock_timestamp()) INTO used;
  IF used>cap THEN RAISE EXCEPTION 'Your plan listing limit is exceeded. Archive listings or upgrade before publishing another listing.' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NEW;
END;$$;
CREATE OR REPLACE FUNCTION assign_provider_post_slot() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE provider uuid; cap integer; assigned integer; row_data jsonb; previous jsonb;
BEGIN
 provider:=NEW.provider_id;IF provider IS NULL THEN RETURN NEW;END IF;
 row_data:=to_jsonb(NEW);previous:=CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
 IF TG_OP='UPDATE' AND NEW.plan_slot IS DISTINCT FROM OLD.plan_slot THEN RAISE EXCEPTION 'Your plan slot cannot be changed directly.' USING ERRCODE='23514';END IF;
 IF TG_OP='INSERT' OR (COALESCE(previous->>'status',previous->>'moderation_status')='archived' AND COALESCE(row_data->>'status',row_data->>'moderation_status')<>'archived') THEN
  PERFORM 1 FROM provider_organizations WHERE id=provider FOR UPDATE;
  SELECT (provider_current_benefits(provider)->>'post_limit')::int INTO cap;
  SELECT n INTO assigned FROM generate_series(1,cap) n WHERE NOT EXISTS(SELECT 1 FROM products p WHERE p.provider_id=provider AND p.moderation_status<>'archived' AND p.id<>NEW.id AND p.plan_slot=n) AND NOT EXISTS(SELECT 1 FROM provider_listings l WHERE l.provider_id=provider AND l.status<>'archived' AND l.id<>NEW.id AND l.plan_slot=n) AND NOT EXISTS(SELECT 1 FROM provider_jobs j WHERE j.provider_id=provider AND j.status='published' AND j.deadline>clock_timestamp() AND j.plan_slot=n) ORDER BY n LIMIT 1;
  IF assigned IS NULL THEN RAISE EXCEPTION 'Your plan post allowance is full. Archive a post or upgrade.' USING ERRCODE='23514';END IF;
  NEW.plan_slot:=assigned;
 END IF;
 RETURN NEW;
END;$$;

-- Add usage metadata without changing existing plan limits or draft counting.
ALTER FUNCTION provider_current_benefits(uuid) RENAME TO provider_current_benefits_before_jobs;
CREATE FUNCTION provider_current_benefits(provider uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT provider_current_benefits_before_jobs(provider)||jsonb_build_object('active_jobs_used',
 (SELECT count(*) FROM provider_jobs WHERE provider_id=provider AND status='published' AND deadline>clock_timestamp()));
$$;
