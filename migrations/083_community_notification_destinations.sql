CREATE OR REPLACE FUNCTION enqueue_user_web_push() RETURNS TRIGGER AS $$
BEGIN
 INSERT INTO web_push_deliveries(subscription_id,source_key,payload)
 SELECT s.id,'notification:'||NEW.id::text,jsonb_build_object('title','Atlantic Express',
 'body',CASE WHEN NEW.type='ticket_reply' THEN 'Support replied to your message.' WHEN NEW.type='social_message' THEN 'You have a new chat message.' WHEN NEW.type='friend_request' THEN 'You have a friend request.' WHEN NEW.type LIKE 'town_hall_%' THEN 'Your Town Hall request has an update.' ELSE 'You have a new account update.' END,
 'tag','notification:'||NEW.id::text,'notification_id',NEW.id::text,'ticket_id',NEW.data->>'ticket_id',
 'chat_room_id',NEW.data->>'chat_room_id','town_hall_room_id',NEW.data->>'town_hall_room_id',
 'community',CASE WHEN NEW.type='friend_request' THEN 'friends' WHEN NEW.type LIKE 'town_hall_%' THEN 'town-hall' ELSE 'chats' END,
 'view',CASE WHEN NEW.type='ticket_reply' THEN 'support' WHEN NEW.type IN ('social_message','friend_request') OR NEW.type LIKE 'town_hall_%' THEN 'messages' ELSE 'overview' END)
 FROM web_push_subscriptions s WHERE s.user_id=NEW.user_id AND s.disabled_at IS NULL ON CONFLICT DO NOTHING;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
