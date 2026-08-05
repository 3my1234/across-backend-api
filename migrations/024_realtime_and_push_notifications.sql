CREATE INDEX IF NOT EXISTS idx_batch_events_created_id
  ON batch_events(created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS user_push_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expo_push_token TEXT NOT NULL UNIQUE,
  platform TEXT NOT NULL CHECK (platform IN ('android', 'ios')),
  disabled_at TIMESTAMPTZ,
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_user_push_tokens_user_active
  ON user_push_tokens(user_id, updated_at DESC)
  WHERE disabled_at IS NULL;

CREATE TABLE IF NOT EXISTS notification_push_deliveries (
  notification_id UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
  push_token_id UUID NOT NULL REFERENCES user_push_tokens(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'sending', 'sent', 'delivered', 'retry', 'failed', 'disabled')),
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expo_ticket_id TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  sent_at TIMESTAMPTZ,
  receipt_checked_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (notification_id, push_token_id)
);

CREATE INDEX IF NOT EXISTS idx_notification_push_due
  ON notification_push_deliveries(next_attempt_at, notification_id, push_token_id)
  WHERE status IN ('pending', 'retry');

CREATE INDEX IF NOT EXISTS idx_notification_push_receipts
  ON notification_push_deliveries(sent_at, expo_ticket_id)
  WHERE status = 'sent' AND receipt_checked_at IS NULL AND expo_ticket_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_notifications_created_id
  ON notifications(created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_orders_user_updated_id
  ON orders(user_id, updated_at DESC, id DESC);
