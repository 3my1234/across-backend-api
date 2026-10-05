ALTER TABLE orders ADD COLUMN IF NOT EXISTS xp_discount integer NOT NULL DEFAULT 0 CHECK (xp_discount >= 0);
CREATE TABLE IF NOT EXISTS xp_redemptions (
  order_id uuid PRIMARY KEY REFERENCES orders(id),
  user_id uuid NOT NULL REFERENCES users(id),
  points integer NOT NULL CHECK (points > 0),
  status text NOT NULL DEFAULT 'reserved' CHECK (status IN ('reserved','consumed','released')),
  expires_at timestamptz NOT NULL DEFAULT now() + interval '15 minutes',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS xp_redemptions_reserved_user ON xp_redemptions(user_id) WHERE status='reserved';
CREATE INDEX IF NOT EXISTS payments_order_lookup ON payments(order_id);
-- Correct previous reward messages without changing earned balances.
UPDATE notifications SET body=body || ' XP can reduce only the Atlantic Express service fee on eligible NGN product orders: 1 XP = NGN 1. It cannot pay for seller products, delivery or Flutterwave charges, and cannot be withdrawn.'
WHERE type IN ('xp_earned','review_request') AND body NOT LIKE '%XP can reduce only%';
