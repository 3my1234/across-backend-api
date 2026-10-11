ALTER TABLE user_push_tokens ADD COLUMN IF NOT EXISTS expo_project_id UUID;
-- HTTP 400 means Expo rejected the entire request before accepting any message.
-- Retry recent unread replies once after deploying project-isolated dispatch.
UPDATE notification_push_deliveries d SET status='retry', attempts=0,
 next_attempt_at=now(), updated_at=now(), last_error='requeued after push request isolation fix'
FROM notifications n, user_push_tokens p
WHERE n.id=d.notification_id AND p.id=d.push_token_id AND p.disabled_at IS NULL
 AND d.status='failed' AND d.last_error='expo push HTTP 400'
 AND n.is_read=false AND n.created_at>now()-interval '48 hours';
