-- Notifications system for buyer communication
CREATE TABLE IF NOT EXISTS notifications (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  order_id UUID REFERENCES orders(id) ON DELETE CASCADE,
  batch_id UUID REFERENCES order_batches(id) ON DELETE SET NULL,
  type TEXT NOT NULL CHECK (type IN (
    'order_confirmed',
    'payment_received',
    'product_purchased',
    'enroute_international',
    'arrived_local',
    'ready_for_pickup',
    'out_for_delivery',
    'delivered',
    'confirm_receipt',
    'review_request',
    'dispute_opened',
    'dispute_resolved',
    'escrow_released',
    'xp_earned',
    'ticket_reply'
  )),
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  data JSONB NOT NULL DEFAULT '{}'::jsonb,
  is_read BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_unread ON notifications(user_id, is_read, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_type_time ON notifications(type, created_at DESC);