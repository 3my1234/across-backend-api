-- Device acknowledgements are separate from private unread-count watermarks.
CREATE TABLE messaging_preferences (
 actor_type TEXT NOT NULL CHECK(actor_type IN ('user','admin')),
 actor_id UUID NOT NULL,
 read_receipts_enabled BOOLEAN NOT NULL DEFAULT true,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(actor_type,actor_id)
);
CREATE TABLE message_receipts (
 channel TEXT NOT NULL CHECK(channel IN ('business','shared','support')),
 conversation_id UUID NOT NULL,
 message_id UUID NOT NULL,
 recipient_type TEXT NOT NULL CHECK(recipient_type IN ('user','admin')),
 recipient_id UUID NOT NULL,
 delivered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 read_at TIMESTAMPTZ,
 PRIMARY KEY(channel,message_id,recipient_type,recipient_id)
);
CREATE INDEX message_receipts_conversation ON message_receipts(channel,conversation_id,message_id);
CREATE INDEX message_receipts_recipient ON message_receipts(recipient_type,recipient_id);
-- Remove exact-location/body-free receipt metadata with its source message.
CREATE FUNCTION delete_message_receipts() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN DELETE FROM message_receipts WHERE channel=TG_ARGV[0] AND message_id=OLD.id; RETURN OLD; END; $$;
CREATE TRIGGER business_receipt_cleanup AFTER DELETE ON provider_conversation_messages FOR EACH ROW EXECUTE FUNCTION delete_message_receipts('business');
CREATE TRIGGER shared_receipt_cleanup AFTER DELETE ON chat_messages FOR EACH ROW EXECUTE FUNCTION delete_message_receipts('shared');
CREATE TRIGGER support_receipt_cleanup AFTER DELETE ON support_messages FOR EACH ROW EXECUTE FUNCTION delete_message_receipts('support');
CREATE FUNCTION delete_actor_message_receipts() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM message_receipts WHERE recipient_type=TG_ARGV[0] AND recipient_id=OLD.id;
 DELETE FROM messaging_preferences WHERE actor_type=TG_ARGV[0] AND actor_id=OLD.id;
 RETURN OLD;
END; $$;
CREATE TRIGGER user_receipt_cleanup AFTER DELETE ON users FOR EACH ROW EXECUTE FUNCTION delete_actor_message_receipts('user');
CREATE TRIGGER admin_receipt_cleanup AFTER DELETE ON admins FOR EACH ROW EXECUTE FUNCTION delete_actor_message_receipts('admin');
