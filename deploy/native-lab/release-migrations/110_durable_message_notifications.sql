-- Opt-in producer path. Existing producers continue inserting notifications inline.
-- Recipients are captured by the authorized message transaction, not looked up
-- later after membership/blocking may have changed.
CREATE TABLE message_notification_jobs (
 event_key text PRIMARY KEY,
 recipient_ids uuid[] NOT NULL,
 type text NOT NULL,
 title text NOT NULL,
 body text NOT NULL,
 data jsonb NOT NULL,
 next_recipient integer NOT NULL DEFAULT 0 CHECK(next_recipient>=0),
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz,
 failed_at timestamptz,
 last_error text,
 CHECK(next_recipient<=cardinality(recipient_ids))
);
CREATE INDEX message_notification_jobs_ready ON message_notification_jobs(available_at,created_at,event_key)
 WHERE completed_at IS NULL AND failed_at IS NULL;
CREATE INDEX message_notification_jobs_completed ON message_notification_jobs(completed_at)
 WHERE completed_at IS NOT NULL;
CREATE INDEX message_notification_jobs_age ON message_notification_jobs(created_at)
 WHERE completed_at IS NULL AND failed_at IS NULL;

CREATE INDEX message_notification_jobs_failed ON message_notification_jobs(failed_at) WHERE failed_at IS NOT NULL;

CREATE TABLE message_notification_policy (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 max_backlog_age_seconds integer NOT NULL DEFAULT 10 CHECK(max_backlog_age_seconds BETWEEN 1 AND 3600)
);
INSERT INTO message_notification_policy DEFAULT VALUES;

-- Indexed age admission, not a serialized global counter or a per-request
-- COUNT of the queue. Never acknowledge another message when notification work
-- is already beyond the configured lag budget. The parent's message tx rolls
-- back on this error. Existing event retries remain idempotent.
CREATE FUNCTION bound_message_notification_backlog() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE oldest timestamptz; max_age integer;
BEGIN
 IF EXISTS(SELECT 1 FROM message_notification_jobs WHERE event_key=NEW.event_key) THEN RETURN NEW; END IF;
 SELECT max_backlog_age_seconds INTO max_age FROM message_notification_policy WHERE singleton;
 SELECT created_at INTO oldest FROM message_notification_jobs WHERE completed_at IS NULL AND failed_at IS NULL ORDER BY created_at LIMIT 1;
 IF oldest<clock_timestamp()-make_interval(secs=>coalesce(max_age,10)) THEN
  RAISE EXCEPTION 'Message notifications are busy; retry with the same message reference' USING ERRCODE='P0503';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER message_notification_backlog_guard BEFORE INSERT ON message_notification_jobs
 FOR EACH ROW EXECUTE FUNCTION bound_message_notification_backlog();

-- Preserve the provider activity row synchronously. Only its per-member native
-- notification fan-out is deferred when the API explicitly opts this tx in.
CREATE OR REPLACE FUNCTION enqueue_provider_native_notification() RETURNS trigger AS $$
DECLARE recipients uuid[]; payload jsonb; message_body text;
BEGIN
 payload:=jsonb_build_object('provider_event_id',NEW.id,'provider_id',NEW.provider_id,'conversation_id',NEW.metadata->>'conversation_id',
 'provider_view',CASE WHEN NEW.event_type IN ('conversation_message','request_created') THEN 'messages' WHEN NEW.event_type LIKE 'listing_%' THEN 'listings' WHEN NEW.event_type LIKE 'product_%' THEN 'products' WHEN NEW.event_type LIKE '%subscription%' THEN 'subscription' WHEN NEW.event_type LIKE '%order%' THEN 'merchant-orders' ELSE 'overview' END);
 message_body:=CASE WHEN NEW.event_type='conversation_message' THEN 'A customer sent you a message.' ELSE 'Your provider workspace has an update.' END;
 IF NEW.event_type='conversation_message' AND current_setting('app.async_message_fanout',true)='on' THEN
  SELECT coalesce(array_agg(pm.user_id ORDER BY pm.user_id),'{}'::uuid[]) INTO recipients
  FROM provider_members pm JOIN users u ON u.id=pm.user_id
  WHERE pm.provider_id=NEW.provider_id AND pm.is_active AND u.is_active;
  INSERT INTO message_notification_jobs(event_key,recipient_ids,type,title,body,data)
  VALUES('provider-native:'||NEW.id::text,recipients,'provider_activity','Atlantic Express',message_body,payload)
  ON CONFLICT(event_key) DO NOTHING;
 ELSE
  INSERT INTO notifications(user_id,type,title,body,data,event_key)
  SELECT pm.user_id,'provider_activity','Atlantic Express',message_body,payload,
  'provider-native:'||NEW.id::text||':'||pm.user_id::text
  FROM provider_members pm JOIN users u ON u.id=pm.user_id
  WHERE pm.provider_id=NEW.provider_id AND pm.is_active AND u.is_active ON CONFLICT(event_key) WHERE event_key IS NOT NULL DO NOTHING;
 END IF;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
