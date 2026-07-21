ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS flutterwave_tx_ref TEXT,
  ADD COLUMN IF NOT EXISTS flutterwave_transaction_id TEXT,
  ADD COLUMN IF NOT EXISTS paid_at TIMESTAMPTZ;

CREATE UNIQUE INDEX IF NOT EXISTS orders_flutterwave_tx_ref_unique
  ON orders(flutterwave_tx_ref)
  WHERE flutterwave_tx_ref IS NOT NULL;

UPDATE orders o
SET flutterwave_tx_ref = el.flutterwave_tx_ref,
    flutterwave_transaction_id = el.flutterwave_transaction_id,
    paid_at = COALESCE(o.paid_at, el.created_at)
FROM escrow_ledger el
WHERE el.order_id = o.id
  AND o.flutterwave_tx_ref IS NULL;