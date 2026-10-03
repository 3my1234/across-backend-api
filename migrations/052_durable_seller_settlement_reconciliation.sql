-- Remember the split recipient used at checkout, independently of later account
-- status changes. Existing accounts cannot be replaced through the public API.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS seller_subaccount_id TEXT NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN IF NOT EXISTS settlement_note TEXT NOT NULL DEFAULT '';
ALTER TABLE merchant_ledger ADD COLUMN IF NOT EXISTS settlement_note TEXT NOT NULL DEFAULT '';

-- Earlier reconciliation wrote zero into net_amount for pending payouts. Restore
-- the expected entitlement without claiming that those funds were released.
UPDATE merchant_ledger SET net_amount=expected_net_amount,updated_at=now()
WHERE settlement_status IN ('pending','on_hold','failed') AND net_amount<>expected_net_amount;
UPDATE payments p SET seller_subaccount_id=pa.flutterwave_subaccount_id
FROM orders o JOIN provider_payout_accounts pa ON pa.provider_id=o.provider_id
WHERE p.order_id=o.id AND p.provider='flutterwave' AND p.purpose='order'
  AND p.seller_subaccount_id='' AND pa.created_at<=p.created_at;

CREATE INDEX IF NOT EXISTS idx_payments_seller_settlement
  ON payments(seller_subaccount_id,paid_at) WHERE purpose='order' AND payment_status='succeeded';

CREATE OR REPLACE FUNCTION protect_payment_seller_split() RETURNS trigger AS $$
BEGIN
  IF OLD.seller_subaccount_id<>'' AND NEW.seller_subaccount_id IS DISTINCT FROM OLD.seller_subaccount_id THEN
    RAISE EXCEPTION 'payment seller split is immutable once assigned';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS trg_protect_payment_seller_split ON payments;
CREATE TRIGGER trg_protect_payment_seller_split BEFORE UPDATE ON payments
FOR EACH ROW EXECUTE FUNCTION protect_payment_seller_split();
CREATE INDEX IF NOT EXISTS idx_merchant_ledger_order_event ON merchant_ledger(order_id,event_key);

CREATE TABLE IF NOT EXISTS seller_settlement_jobs (
  subaccount_id TEXT PRIMARY KEY,
  from_date DATE NOT NULL,
  to_date DATE NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')::date,
  list_page INTEGER NOT NULL DEFAULT 1 CHECK(list_page>0),
  settlement_ids TEXT[] NOT NULL DEFAULT '{}',
  detail_page INTEGER NOT NULL DEFAULT 1 CHECK(detail_page>0),
  detail_seen INTEGER NOT NULL DEFAULT 0,
  detail_hash TEXT NOT NULL DEFAULT '',
  list_hash TEXT NOT NULL DEFAULT '',
  list_finished BOOLEAN NOT NULL DEFAULT false,
  next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  locked_until TIMESTAMPTZ,
  lease_token UUID,
  checked_at TIMESTAMPTZ,
  failure_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_seller_settlement_jobs_due
  ON seller_settlement_jobs(next_check_at,subaccount_id);

-- Safe backfill: enqueue verified historical charges, never infer bank credit
-- from a charge, and never reset an already imported payout on redeployment.
INSERT INTO seller_settlement_jobs(subaccount_id,from_date)
SELECT seller_subaccount_id,MIN((created_at AT TIME ZONE 'UTC')::date)
FROM payments WHERE provider='flutterwave' AND purpose='order'
  AND payment_status='succeeded' AND seller_subaccount_id<>''
GROUP BY seller_subaccount_id ON CONFLICT DO NOTHING;
