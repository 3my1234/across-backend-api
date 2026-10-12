CREATE TABLE IF NOT EXISTS web_push_subscriptions (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 endpoint TEXT NOT NULL UNIQUE,p256dh TEXT NOT NULL,auth TEXT NOT NULL,vapid_public_key TEXT NOT NULL,
 disabled_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS web_push_user_active ON web_push_subscriptions(user_id) WHERE disabled_at IS NULL;
CREATE TABLE IF NOT EXISTS web_push_deliveries (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),subscription_id UUID NOT NULL REFERENCES web_push_subscriptions(id) ON DELETE CASCADE,
 source_key TEXT NOT NULL,payload JSONB NOT NULL,status TEXT NOT NULL DEFAULT 'pending',attempts INT NOT NULL DEFAULT 0,
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),last_error TEXT NOT NULL DEFAULT '',sent_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),UNIQUE(subscription_id,source_key));
CREATE INDEX IF NOT EXISTS web_push_due ON web_push_deliveries(next_attempt_at,id) WHERE status IN ('pending','retry');
CREATE OR REPLACE FUNCTION enqueue_provider_web_push() RETURNS TRIGGER AS $$
BEGIN
 INSERT INTO web_push_deliveries(subscription_id,source_key,payload)
 SELECT s.id,'provider-event:'||NEW.id::text,jsonb_build_object('title','Atlantic Express',
 'body',CASE WHEN NEW.event_type='conversation_message' THEN 'A customer sent you a message.' WHEN NEW.event_type='request_created' THEN 'You have a new booking or enquiry.' ELSE 'Your business has a new update.' END,
 'tag','provider-event:'||NEW.id::text,'view',CASE WHEN NEW.event_type='conversation_message' THEN 'messages' WHEN NEW.event_type='request_created' THEN 'requests' ELSE 'overview' END,
 'conversation_id',NEW.metadata->>'conversation_id')
 FROM web_push_subscriptions s JOIN provider_members pm ON pm.user_id=s.user_id
 WHERE pm.provider_id=NEW.provider_id AND pm.is_active AND s.disabled_at IS NULL ON CONFLICT DO NOTHING;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS provider_web_push_event ON provider_marketplace_events;
CREATE TRIGGER provider_web_push_event AFTER INSERT ON provider_marketplace_events FOR EACH ROW EXECUTE FUNCTION enqueue_provider_web_push();
CREATE OR REPLACE FUNCTION enqueue_user_web_push() RETURNS TRIGGER AS $$
BEGIN
 INSERT INTO web_push_deliveries(subscription_id,source_key,payload)
 SELECT s.id,'notification:'||NEW.id::text,jsonb_build_object('title','Atlantic Express','body',CASE WHEN NEW.type='ticket_reply' THEN 'Support replied to your message.' ELSE 'You have a new account update.' END,
 'tag','notification:'||NEW.id::text,'view',CASE WHEN NEW.type='ticket_reply' THEN 'support' ELSE 'overview' END)
 FROM web_push_subscriptions s WHERE s.user_id=NEW.user_id AND s.disabled_at IS NULL ON CONFLICT DO NOTHING;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS user_web_push_notification ON notifications;
CREATE TRIGGER user_web_push_notification AFTER INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION enqueue_user_web_push();
