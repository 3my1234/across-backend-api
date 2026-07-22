CREATE TABLE IF NOT EXISTS user_identities (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  provider_subject TEXT NOT NULL,
  email TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(provider, provider_subject)
);

CREATE INDEX IF NOT EXISTS idx_user_identities_user ON user_identities(user_id);

INSERT INTO user_identities(user_id, provider, provider_subject, email)
SELECT id, 'privy', privy_user_id, email
FROM users
WHERE privy_user_id IS NOT NULL AND trim(privy_user_id) <> ''
ON CONFLICT (provider, provider_subject) DO NOTHING;

ALTER TABLE notifications ADD COLUMN IF NOT EXISTS event_key TEXT;

DELETE FROM notifications
WHERE data ? 'verification_required'
   OR title IN ('Verify your email', 'Verification email resent');

DELETE FROM notifications a
USING notifications b
WHERE a.id > b.id
  AND a.event_key IS NOT NULL
  AND a.event_key = b.event_key;

CREATE UNIQUE INDEX IF NOT EXISTS notifications_event_key_unique
  ON notifications(event_key)
  WHERE event_key IS NOT NULL;

DELETE FROM xp_transactions a
USING xp_transactions b
WHERE a.id > b.id
  AND a.user_id = b.user_id
  AND a.reason = b.reason
  AND COALESCE(a.reference_id, '') = COALESCE(b.reference_id, '')
  AND COALESCE(a.reference_id, '') <> '';

CREATE UNIQUE INDEX IF NOT EXISTS xp_transactions_event_unique
  ON xp_transactions(user_id, reason, reference_id)
  WHERE reference_id IS NOT NULL AND reference_id <> '';

WITH eligible AS (
  SELECT id AS user_id
  FROM users
  WHERE is_active = true AND email_verified = true
), awarded AS (
  INSERT INTO xp_transactions(user_id, amount, reason, reference_id)
  SELECT user_id, 100, 'welcome', 'account-welcome'
  FROM eligible
  ON CONFLICT DO NOTHING
  RETURNING user_id
)
INSERT INTO notifications(user_id, type, title, body, data, event_key)
SELECT user_id, 'xp_earned', 'Welcome to Atlantic Express - 100 XP earned',
       'You received 100 XP, worth N100 in discounts. Earn more XP through daily logins and completed purchases.',
       jsonb_build_object('xp', 100, 'naira_value', 100, 'reason', 'welcome'),
       'welcome-xp:' || user_id
FROM awarded
ON CONFLICT DO NOTHING;

UPDATE xp_transactions xt
SET amount = CASE
  WHEN o.total_amount < 1000 THEN 10
  WHEN o.total_amount < 10000 THEN 100
  WHEN o.total_amount < 100000 THEN 500
  WHEN o.total_amount < 500000 THEN 1000
  ELSE 2500
END
FROM orders o
WHERE xt.user_id = o.user_id
  AND xt.reason = 'purchase'
  AND xt.reference_id = 'purchase-' || o.id;

WITH paid_orders AS (
  SELECT o.id, o.user_id,
         CASE
           WHEN o.total_amount < 1000 THEN 10
           WHEN o.total_amount < 10000 THEN 100
           WHEN o.total_amount < 100000 THEN 500
           WHEN o.total_amount < 500000 THEN 1000
           ELSE 2500
         END AS xp
  FROM orders o
  WHERE o.order_status IN ('Paid', 'Shipped', 'Delivered', 'Completed')
), awarded AS (
  INSERT INTO xp_transactions(user_id, amount, reason, reference_id)
  SELECT user_id, xp, 'purchase', 'purchase-' || id
  FROM paid_orders
  ON CONFLICT DO NOTHING
  RETURNING user_id, amount, substring(reference_id FROM 10)::uuid AS order_id
)
INSERT INTO notifications(user_id, order_id, type, title, body, data, event_key)
SELECT user_id, order_id, 'xp_earned', 'Purchase reward earned',
       'You earned ' || amount || ' XP from your completed purchase.',
       jsonb_build_object('xp', amount, 'naira_value', amount, 'reason', 'purchase'),
       'purchase-xp:' || order_id
FROM awarded
ON CONFLICT DO NOTHING;
