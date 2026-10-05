CREATE INDEX IF NOT EXISTS support_messages_ticket_cursor ON support_messages(ticket_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_user_cursor ON support_tickets(user_id,created_at DESC,id DESC);
