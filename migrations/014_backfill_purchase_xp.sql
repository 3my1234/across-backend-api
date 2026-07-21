WITH awarded AS (
  INSERT INTO xp_transactions(user_id, amount, reason, reference_id)
  SELECT o.user_id, 50, 'purchase', 'purchase-' || o.id
  FROM orders o
  WHERE o.order_status IN ('Paid', 'Shipped', 'Delivered', 'Completed')
    AND NOT EXISTS (
      SELECT 1
      FROM xp_transactions xt
      WHERE xt.user_id = o.user_id
        AND xt.reference_id = 'purchase-' || o.id
    )
  RETURNING user_id, substring(reference_id FROM 10)::uuid AS order_id
)
INSERT INTO notifications(user_id, order_id, type, title, body, data)
SELECT user_id, order_id, 'xp_earned', 'Purchase XP earned',
  'You earned 50 XP for your purchase!', jsonb_build_object('xp', 50)
FROM awarded;