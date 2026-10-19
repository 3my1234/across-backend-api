-- A reply references a stored message; clients cannot supply a quoted author/body.
ALTER TABLE chat_messages ADD COLUMN reply_to_id uuid REFERENCES chat_messages(id) ON DELETE SET NULL;
CREATE INDEX chat_message_replies ON chat_messages(reply_to_id) WHERE reply_to_id IS NOT NULL;
