CREATE OR REPLACE FUNCTION enqueue_provider_web_push() RETURNS TRIGGER AS $$
BEGIN
 INSERT INTO web_push_deliveries(subscription_id,source_key,payload)
 SELECT s.id,'provider-event:'||NEW.id::text,jsonb_build_object('title','Atlantic Express',
 'body',CASE WHEN NEW.event_type='conversation_message' THEN 'A customer sent you a message.' WHEN NEW.event_type='request_created' THEN 'You have a new booking or enquiry.' ELSE 'Your business has a new update.' END,
 'tag','provider-event:'||NEW.id::text,'event_id',NEW.id::text,
 'view',CASE WHEN NEW.event_type='conversation_message' THEN 'messages' WHEN NEW.event_type='request_created' THEN 'requests' WHEN NEW.event_type LIKE 'listing_%' THEN 'listings' WHEN NEW.event_type LIKE 'product_%' THEN 'products' WHEN NEW.event_type LIKE '%order%' THEN 'merchant-orders' WHEN NEW.event_type LIKE '%subscription%' THEN 'subscription' ELSE 'overview' END,
 'conversation_id',NEW.metadata->>'conversation_id')
 FROM web_push_subscriptions s JOIN provider_members pm ON pm.user_id=s.user_id
 WHERE pm.provider_id=NEW.provider_id AND pm.is_active AND s.disabled_at IS NULL ON CONFLICT DO NOTHING;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE OR REPLACE FUNCTION enqueue_user_web_push() RETURNS TRIGGER AS $$
BEGIN
 INSERT INTO web_push_deliveries(subscription_id,source_key,payload)
 SELECT s.id,'notification:'||NEW.id::text,jsonb_build_object('title','Atlantic Express',
 'body',CASE WHEN NEW.type='ticket_reply' THEN 'Support replied to your message.' ELSE 'You have a new account update.' END,
 'tag','notification:'||NEW.id::text,'notification_id',NEW.id::text,'ticket_id',NEW.data->>'ticket_id',
 'view',CASE WHEN NEW.type='ticket_reply' THEN 'support' ELSE 'overview' END)
 FROM web_push_subscriptions s WHERE s.user_id=NEW.user_id AND s.disabled_at IS NULL ON CONFLICT DO NOTHING;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
