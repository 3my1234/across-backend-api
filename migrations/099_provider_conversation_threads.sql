-- Preserve every existing conversation/message id and product association.
-- thread_id is the stable inbox identity shared by one customer and business.
ALTER TABLE provider_conversations ADD COLUMN thread_id UUID;
WITH ranked AS (
 SELECT id,first_value(id) OVER(PARTITION BY provider_id,user_id ORDER BY created_at,id) AS root
 FROM provider_conversations
) UPDATE provider_conversations c SET thread_id=r.root FROM ranked r WHERE r.id=c.id;
ALTER TABLE provider_conversations ALTER COLUMN thread_id SET NOT NULL;
ALTER TABLE provider_conversations ADD CONSTRAINT provider_thread_root_fk
 FOREIGN KEY(thread_id) REFERENCES provider_conversations(id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX provider_conversations_pair_thread ON provider_conversations(provider_id,user_id,thread_id);
ALTER TABLE provider_conversation_messages ADD COLUMN context JSONB NOT NULL DEFAULT '{}';
UPDATE provider_conversation_messages msg SET context=jsonb_build_object('listing_id',c.listing_id,'product_id',c.product_id,'title',COALESCE(l.title,p.title,''))
FROM provider_conversations c LEFT JOIN provider_listings l ON l.id=c.listing_id LEFT JOIN products p ON p.id=c.product_id WHERE msg.conversation_id=c.id;
CREATE FUNCTION provider_message_context() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 SELECT jsonb_build_object('listing_id',c.listing_id,'product_id',c.product_id,'title',COALESCE(l.title,p.title,'')) INTO NEW.context
 FROM provider_conversations c LEFT JOIN provider_listings l ON l.id=c.listing_id LEFT JOIN products p ON p.id=c.product_id WHERE c.id=NEW.conversation_id;
 RETURN NEW;
END $$;
CREATE TRIGGER provider_message_context BEFORE INSERT ON provider_conversation_messages
FOR EACH ROW EXECUTE FUNCTION provider_message_context();

-- Serialize concurrent first contacts for different products of the same business.
CREATE FUNCTION assign_provider_conversation_thread() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended(NEW.provider_id::text||':'||NEW.user_id::text,0));
 SELECT thread_id INTO NEW.thread_id FROM provider_conversations
 WHERE provider_id=NEW.provider_id AND user_id=NEW.user_id ORDER BY created_at,id LIMIT 1;
 NEW.thread_id:=COALESCE(NEW.thread_id,NEW.id);
 RETURN NEW;
END $$;
CREATE TRIGGER provider_conversation_thread BEFORE INSERT ON provider_conversations
FOR EACH ROW EXECUTE FUNCTION assign_provider_conversation_thread();

-- Existing indexes order inbox rows by last_message_at. Keep the representative
-- current without changing physical message ownership or per-message context.
CREATE FUNCTION touch_provider_conversation_thread() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE provider_conversations SET last_message_at=GREATEST(last_message_at,NEW.created_at)
 WHERE id=(SELECT thread_id FROM provider_conversations WHERE id=NEW.conversation_id);
 RETURN NEW;
END $$;
CREATE TRIGGER provider_conversation_thread_message AFTER INSERT ON provider_conversation_messages
FOR EACH ROW EXECUTE FUNCTION touch_provider_conversation_thread();
WITH aggregate AS (
 SELECT thread_id,max(last_message_at) AS latest,bool_or(status='blocked') AS blocked
 FROM provider_conversations GROUP BY thread_id
) UPDATE provider_conversations c SET last_message_at=a.latest,
 status=CASE WHEN a.blocked THEN 'blocked' ELSE c.status END
 FROM aggregate a WHERE c.id=a.thread_id;
