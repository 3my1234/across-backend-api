ALTER TABLE support_tickets ADD COLUMN IF NOT EXISTS user_last_read_at TIMESTAMPTZ;
ALTER TABLE support_tickets ADD COLUMN IF NOT EXISTS admin_last_read_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS support_messages_unread_idx ON support_messages(ticket_id,sender_type,created_at);
