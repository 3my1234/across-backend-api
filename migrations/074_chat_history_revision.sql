-- A revision changes on every committed message, including backdated booking
-- messages and transactions whose commit order differs from timestamp order.
ALTER TABLE provider_conversations ADD COLUMN IF NOT EXISTS history_revision BIGINT NOT NULL DEFAULT 0;
CREATE OR REPLACE FUNCTION bump_provider_chat_revision() RETURNS TRIGGER AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  UPDATE provider_conversations SET history_revision=history_revision+1 WHERE id=OLD.conversation_id;
  RETURN OLD;
 END IF;
 UPDATE provider_conversations SET history_revision=history_revision+1 WHERE id=NEW.conversation_id;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS provider_chat_revision ON provider_conversation_messages;
CREATE TRIGGER provider_chat_revision AFTER INSERT OR UPDATE OR DELETE ON provider_conversation_messages
 FOR EACH ROW EXECUTE FUNCTION bump_provider_chat_revision();
