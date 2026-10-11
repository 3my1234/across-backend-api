-- Read-only diagnostics; no push tokens, message text or secret keys.
SELECT platform, sound_enabled, count(*) AS active_devices,
       max(last_seen_at) AS latest_registration
FROM user_push_tokens WHERE disabled_at IS NULL GROUP BY platform, sound_enabled;

SELECT d.status, count(*) AS deliveries, max(d.updated_at) AS latest_update
FROM notification_push_deliveries d JOIN notifications n ON n.id=d.notification_id
WHERE n.created_at > now()-interval '24 hours' GROUP BY d.status;

SELECT n.type, n.created_at, d.status, d.attempts, d.last_error,
       d.sent_at, d.receipt_checked_at
FROM notifications n LEFT JOIN notification_push_deliveries d ON d.notification_id=n.id
WHERE n.type='ticket_reply' AND n.created_at>now()-interval '24 hours'
ORDER BY n.created_at DESC LIMIT 20;
