-- Reuse the existing user notification/Expo outbox for the provider workspace.
CREATE OR REPLACE FUNCTION enqueue_provider_native_notification() RETURNS trigger AS $$
BEGIN
 INSERT INTO notifications(user_id,type,title,body,data,event_key)
 SELECT pm.user_id,'provider_activity','Atlantic Express',
 CASE WHEN NEW.event_type='conversation_message' THEN 'A customer sent you a message.' ELSE 'Your provider workspace has an update.' END,
 jsonb_build_object('provider_event_id',NEW.id,'provider_id',NEW.provider_id,'conversation_id',NEW.metadata->>'conversation_id',
 'provider_view',CASE WHEN NEW.event_type IN ('conversation_message','request_created') THEN 'messages' WHEN NEW.event_type LIKE 'listing_%' THEN 'listings' WHEN NEW.event_type LIKE 'product_%' THEN 'products' WHEN NEW.event_type LIKE '%subscription%' THEN 'subscription' WHEN NEW.event_type LIKE '%order%' THEN 'merchant-orders' ELSE 'overview' END),
 'provider-native:'||NEW.id::text||':'||pm.user_id::text
 FROM provider_members pm JOIN users u ON u.id=pm.user_id
 WHERE pm.provider_id=NEW.provider_id AND pm.is_active AND u.is_active ON CONFLICT(event_key) DO NOTHING;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER provider_native_notification AFTER INSERT ON provider_marketplace_events FOR EACH ROW EXECUTE FUNCTION enqueue_provider_native_notification();

-- Provider events already generate web push. Do not duplicate it through the
-- native user's notification row; support and buyer notifications still use this.
CREATE OR REPLACE FUNCTION enqueue_user_web_push() RETURNS trigger AS $$
BEGIN
 IF NEW.data ? 'provider_event_id' THEN RETURN NEW; END IF;
 INSERT INTO web_push_deliveries(subscription_id,source_key,payload)
 SELECT s.id,'notification:'||NEW.id::text,jsonb_build_object('title','Atlantic Express',
 'body',CASE WHEN NEW.type='ticket_reply' THEN 'Support replied to your message.' WHEN NEW.type='social_message' THEN 'You have a new chat message.' WHEN NEW.type LIKE 'town_hall_%' THEN 'Your Town Hall request has an update.' ELSE 'You have an account update.' END,
 'tag','notification:'||NEW.id::text,'notification_id',NEW.id::text,'ticket_id',NEW.data->>'ticket_id',
 'conversation_id',NEW.data->>'conversation_id','chat_room_id',NEW.data->>'chat_room_id','town_hall_room_id',NEW.data->>'town_hall_room_id',
 'view',CASE WHEN NEW.type='ticket_reply' THEN 'support' WHEN NEW.type IN('social_message','marketplace_message') OR NEW.type LIKE 'town_hall_%' THEN 'messages' ELSE 'overview' END,
 'community',CASE WHEN NEW.type LIKE 'town_hall_%' THEN 'town-hall' ELSE 'chats' END)
 FROM web_push_subscriptions s WHERE s.user_id=NEW.user_id AND s.disabled_at IS NULL ON CONFLICT DO NOTHING;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
