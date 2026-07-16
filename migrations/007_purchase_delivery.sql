-- Per-order purchase tracking for Admin II
ALTER TABLE order_items
  ADD COLUMN IF NOT EXISTS purchase_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (purchase_status IN ('pending', 'purchased', 'failed')),
  ADD COLUMN IF NOT EXISTS purchase_notes TEXT NOT NULL DEFAULT '';

-- Delivery management for Admin III
ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS pickup_location TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS pickup_phone TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS delivery_notes TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS confirmed_at TIMESTAMPTZ;

-- Review rewards tracking
CREATE TABLE IF NOT EXISTS review_rewards (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  reward_amount NUMERIC(14,2) NOT NULL DEFAULT 500,
  reward_currency CHAR(3) NOT NULL DEFAULT 'NGN',
  is_claimed BOOLEAN NOT NULL DEFAULT false,
  claimed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(user_id, order_id)
);

-- Delivery confirmation tracking
ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS delivery_confirmed BOOLEAN NOT NULL DEFAULT false;