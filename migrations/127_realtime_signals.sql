-- Default off. Signals contain only random event IDs and recipient user IDs;
-- no message, location, token or business information travels via NOTIFY.
-- PostgreSQL delivers NOTIFY only after the modifying transaction commits.
CREATE TABLE realtime_signal_policy (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 enabled boolean NOT NULL DEFAULT false
);
INSERT INTO realtime_signal_policy(singleton,enabled) VALUES(true,false);

CREATE FUNCTION atl_realtime_emit(recipient uuid) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 IF recipient IS NOT NULL THEN
  PERFORM pg_notify('atl_realtime_wake',gen_random_uuid()::text||':'||recipient::text);
 END IF;
END $$;

CREATE FUNCTION atl_realtime_emit_hall() RETURNS void LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_notify('atl_realtime_wake',gen_random_uuid()::text||':hall'); END $$;

CREATE FUNCTION atl_realtime_shared_room_signal() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target uuid; rid uuid;
BEGIN
 IF NOT (SELECT enabled FROM realtime_signal_policy WHERE singleton) THEN RETURN NEW; END IF;
 IF TG_TABLE_NAME='chat_rooms' THEN rid:=NEW.id; ELSE rid:=NEW.room_id; END IF;
 FOR target IN SELECT DISTINCT p.user_id FROM chat_participants p WHERE p.room_id=rid AND p.state='active' LOOP
  PERFORM atl_realtime_emit(target);
 END LOOP;
 -- A removed participant must also receive a wake-up; the protected HTTP
 -- endpoint decides what they may see after their access changes.
 IF TG_TABLE_NAME='chat_participants' THEN PERFORM atl_realtime_emit(NEW.user_id); END IF;
 IF TG_TABLE_NAME='town_hall_applications' THEN PERFORM atl_realtime_emit(NEW.requester_user_id); END IF;
 IF TG_TABLE_NAME='chat_rooms' THEN
  IF NEW.kind='town_hall' AND OLD.state IS DISTINCT FROM NEW.state THEN PERFORM atl_realtime_emit_hall(); END IF;
 END IF;
 RETURN NEW;
END $$;

CREATE TRIGGER realtime_shared_message AFTER INSERT ON chat_messages
 FOR EACH ROW EXECUTE FUNCTION atl_realtime_shared_room_signal();
CREATE TRIGGER realtime_shared_participant_insert AFTER INSERT ON chat_participants
 FOR EACH ROW EXECUTE FUNCTION atl_realtime_shared_room_signal();
CREATE TRIGGER realtime_shared_participant_update AFTER UPDATE OF state,last_read_at ON chat_participants
 FOR EACH ROW WHEN (OLD.state IS DISTINCT FROM NEW.state OR OLD.last_read_at IS DISTINCT FROM NEW.last_read_at)
 EXECUTE FUNCTION atl_realtime_shared_room_signal();
CREATE TRIGGER realtime_hall_application AFTER INSERT OR UPDATE OF state ON town_hall_applications
 FOR EACH ROW EXECUTE FUNCTION atl_realtime_shared_room_signal();
CREATE TRIGGER realtime_hall_offer AFTER INSERT OR UPDATE OF unit_price,terms ON town_hall_offers
 FOR EACH ROW EXECUTE FUNCTION atl_realtime_shared_room_signal();
CREATE TRIGGER realtime_shared_room_update AFTER UPDATE OF state,context ON chat_rooms
 FOR EACH ROW WHEN (OLD.state IS DISTINCT FROM NEW.state OR OLD.context IS DISTINCT FROM NEW.context)
 EXECUTE FUNCTION atl_realtime_shared_room_signal();

CREATE FUNCTION atl_realtime_hall_discovery_signal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (SELECT enabled FROM realtime_signal_policy WHERE singleton) THEN PERFORM atl_realtime_emit_hall(); END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER realtime_hall_created AFTER INSERT ON town_hall_requests
 FOR EACH ROW EXECUTE FUNCTION atl_realtime_hall_discovery_signal();

CREATE FUNCTION atl_realtime_business_signal() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target uuid; cid uuid;
BEGIN
 IF NOT (SELECT enabled FROM realtime_signal_policy WHERE singleton) THEN RETURN NEW; END IF;
 IF TG_TABLE_NAME='provider_conversations' THEN cid:=NEW.id; ELSE cid:=NEW.conversation_id; END IF;
 FOR target IN
  SELECT c.user_id FROM provider_conversations c WHERE c.id=cid
  UNION SELECT pm.user_id FROM provider_conversations c JOIN provider_members pm ON pm.provider_id=c.provider_id AND pm.is_active WHERE c.id=cid
 LOOP PERFORM atl_realtime_emit(target); END LOOP;
 RETURN NEW;
END $$;
CREATE TRIGGER realtime_business_message AFTER INSERT ON provider_conversation_messages
 FOR EACH ROW EXECUTE FUNCTION atl_realtime_business_signal();
CREATE TRIGGER realtime_business_read AFTER UPDATE OF buyer_last_read_at,provider_last_read_at ON provider_conversations
 FOR EACH ROW WHEN (OLD.buyer_last_read_at IS DISTINCT FROM NEW.buyer_last_read_at OR OLD.provider_last_read_at IS DISTINCT FROM NEW.provider_last_read_at)
 EXECUTE FUNCTION atl_realtime_business_signal();

CREATE FUNCTION atl_realtime_notification_signal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (SELECT enabled FROM realtime_signal_policy WHERE singleton) THEN PERFORM atl_realtime_emit(NEW.user_id); END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER realtime_notification AFTER INSERT OR UPDATE OF is_read ON notifications
 FOR EACH ROW EXECUTE FUNCTION atl_realtime_notification_signal();

-- Receipt changes notify the original sender only. Re-reading a receipt does
-- not create another receipt and therefore cannot create an event loop.
CREATE FUNCTION atl_realtime_receipt_signal() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE sender uuid;
BEGIN
 IF NOT (SELECT enabled FROM realtime_signal_policy WHERE singleton) THEN RETURN NEW; END IF;
 IF NEW.channel='shared' THEN SELECT sender_user_id INTO sender FROM chat_messages WHERE id=NEW.message_id;
 ELSIF NEW.channel='business' THEN SELECT sender_user_id INTO sender FROM provider_conversation_messages WHERE id=NEW.message_id;
 END IF;
 IF sender IS DISTINCT FROM NEW.recipient_id THEN PERFORM atl_realtime_emit(sender); END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER realtime_receipt_insert AFTER INSERT ON message_receipts
 FOR EACH ROW EXECUTE FUNCTION atl_realtime_receipt_signal();
CREATE TRIGGER realtime_receipt_update AFTER UPDATE OF delivered_at,read_at ON message_receipts
 FOR EACH ROW WHEN (OLD.delivered_at IS DISTINCT FROM NEW.delivered_at OR OLD.read_at IS DISTINCT FROM NEW.read_at)
 EXECUTE FUNCTION atl_realtime_receipt_signal();
