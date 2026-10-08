ALTER TABLE products ADD COLUMN payment_mode TEXT NOT NULL DEFAULT 'flutterwave'
 CHECK (payment_mode IN ('flutterwave','contact','both'));

ALTER TABLE provider_conversations ALTER COLUMN listing_id DROP NOT NULL;
ALTER TABLE provider_conversations ADD COLUMN product_id UUID REFERENCES products(id) ON DELETE CASCADE;
ALTER TABLE provider_conversations ADD CONSTRAINT conversation_subject_check
 CHECK ((listing_id IS NOT NULL)::int + (product_id IS NOT NULL)::int = 1);
CREATE UNIQUE INDEX provider_conversation_product_buyer ON provider_conversations(product_id,user_id)
 WHERE product_id IS NOT NULL;
ALTER TABLE provider_conversation_messages ADD COLUMN media_keys TEXT[] NOT NULL DEFAULT '{}'
 CHECK (cardinality(media_keys)<=4);
ALTER TABLE provider_conversation_messages ADD COLUMN client_message_id UUID;
CREATE UNIQUE INDEX provider_message_client_id ON provider_conversation_messages(sender_user_id,client_message_id)
 WHERE client_message_id IS NOT NULL;

ALTER TABLE provider_subscriptions ADD COLUMN billing_mode TEXT NOT NULL DEFAULT 'recurring' CHECK (billing_mode IN ('recurring','one_time'));
