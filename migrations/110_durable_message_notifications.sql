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
 last_error text,
 CHECK(next_recipient<=cardinality(recipient_ids))
);
CREATE INDEX message_notification_jobs_ready ON message_notification_jobs(available_at,created_at,event_key)
 WHERE completed_at IS NULL;
CREATE INDEX message_notification_jobs_completed ON message_notification_jobs(completed_at)
 WHERE completed_at IS NOT NULL;

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
