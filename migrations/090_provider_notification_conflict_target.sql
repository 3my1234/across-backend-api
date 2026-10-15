-- Match the partial unique index created by 018/schema.sql. The notification
-- trigger runs inside the message transaction; an invalid conflict target
-- would roll back both the message and its provider event.
CREATE OR REPLACE FUNCTION enqueue_provider_native_notification() RETURNS trigger AS $$
BEGIN
 INSERT INTO notifications(user_id,type,title,body,data,event_key)
 SELECT pm.user_id,'provider_activity','Atlantic Express',
 CASE WHEN NEW.event_type='conversation_message' THEN 'A customer sent you a message.' ELSE 'Your provider workspace has an update.' END,
 jsonb_build_object('provider_event_id',NEW.id,'provider_id',NEW.provider_id,'conversation_id',NEW.metadata->>'conversation_id',
 'provider_view',CASE WHEN NEW.event_type IN ('conversation_message','request_created') THEN 'messages' WHEN NEW.event_type LIKE 'listing_%' THEN 'listings' WHEN NEW.event_type LIKE 'product_%' THEN 'products' WHEN NEW.event_type LIKE '%subscription%' THEN 'subscription' WHEN NEW.event_type LIKE '%order%' THEN 'merchant-orders' ELSE 'overview' END),
 'provider-native:'||NEW.id::text||':'||pm.user_id::text
 FROM provider_members pm JOIN users u ON u.id=pm.user_id
 WHERE pm.provider_id=NEW.provider_id AND pm.is_active AND u.is_active
 ON CONFLICT(event_key) WHERE event_key IS NOT NULL DO NOTHING;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
