-- Unread counts constrain both the conversation and sender before the timestamp range.
CREATE INDEX IF NOT EXISTS provider_conversation_unread_idx
 ON provider_conversation_messages(conversation_id,sender_type,created_at);
