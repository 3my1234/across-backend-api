ALTER TABLE provider_conversation_messages
 ADD COLUMN IF NOT EXISTS source_request_id UUID REFERENCES provider_requests(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS provider_message_source_request
 ON provider_conversation_messages(source_request_id) WHERE source_request_id IS NOT NULL;

-- Preserve existing chat status/read markers and attach historical contacts.
INSERT INTO provider_conversations(listing_id,provider_id,user_id,last_message_at,created_at,updated_at)
SELECT listing_id,provider_id,user_id,max(created_at),min(created_at),max(created_at)
FROM provider_requests GROUP BY listing_id,provider_id,user_id
ON CONFLICT(listing_id,user_id) DO NOTHING;

INSERT INTO provider_conversation_messages(conversation_id,sender_type,sender_user_id,body,created_at,source_request_id)
SELECT c.id,'buyer',r.user_id,
 left(initcap(r.request_type)||' request'||CASE WHEN btrim(r.message)<>'' THEN ': '||btrim(r.message) ELSE '' END,2000),r.created_at,r.id
FROM provider_requests r JOIN provider_conversations c ON c.listing_id=r.listing_id AND c.user_id=r.user_id
ON CONFLICT(source_request_id) WHERE source_request_id IS NOT NULL DO NOTHING;

UPDATE provider_conversations c SET last_message_at=latest.created_at
FROM (SELECT conversation_id,max(created_at) created_at FROM provider_conversation_messages GROUP BY conversation_id) latest
WHERE c.id=latest.conversation_id AND c.last_message_at<latest.created_at;

CREATE INDEX IF NOT EXISTS support_tickets_updated_cursor ON support_tickets(updated_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_status_updated_cursor ON support_tickets(status,updated_at DESC,id DESC);
