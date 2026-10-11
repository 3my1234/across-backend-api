-- Negotiate the audible Android channel per installation. Older APKs keep orders.
ALTER TABLE user_push_tokens ADD COLUMN IF NOT EXISTS notification_channel TEXT NOT NULL DEFAULT 'orders';
ALTER TABLE user_push_tokens ADD CONSTRAINT user_push_tokens_notification_channel_check
 CHECK (notification_channel IN ('orders', 'atl-updates-v2'));
