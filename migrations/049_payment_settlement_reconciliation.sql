-- Keep authorization, split allocation and actual Flutterwave payout separate.
-- A successful charge is not proof that a seller's bank account was credited.

ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_settlement_status_check;
ALTER TABLE payments
  ADD CONSTRAINT payments_settlement_status_check
  CHECK (settlement_status IN ('pending','on_hold','settled','failed','reversed')),
  ADD COLUMN IF NOT EXISTS charged_amount NUMERIC(14,2),
  ADD COLUMN IF NOT EXISTS gateway_fee NUMERIC(14,2) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS merchant_fee NUMERIC(14,2) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS provider_settlement_amount NUMERIC(14,2),
  ADD COLUMN IF NOT EXISTS provider_settlement_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS settlement_reference TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS settlement_destination TEXT NOT NULL DEFAULT '';

ALTER TABLE merchant_ledger
  ADD COLUMN IF NOT EXISTS expected_net_amount NUMERIC(14,2),
  ADD COLUMN IF NOT EXISTS gateway_deductions NUMERIC(14,2) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS settlement_amount NUMERIC(14,2),
  ADD COLUMN IF NOT EXISTS settlement_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS settlement_status TEXT NOT NULL DEFAULT 'pending',
  ADD COLUMN IF NOT EXISTS settlement_reference TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS settlement_destination TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS settlement_checked_at TIMESTAMPTZ;

UPDATE merchant_ledger
SET expected_net_amount = net_amount
WHERE expected_net_amount IS NULL;

ALTER TABLE merchant_ledger
  ALTER COLUMN expected_net_amount SET NOT NULL,
  DROP CONSTRAINT IF EXISTS merchant_ledger_settlement_status_check;
ALTER TABLE merchant_ledger
  ADD CONSTRAINT merchant_ledger_settlement_status_check
  CHECK (settlement_status IN ('pending','on_hold','settled','failed','reversed'));

CREATE INDEX IF NOT EXISTS idx_payments_pending_settlement
  ON payments(settlement_status, paid_at)
  WHERE purpose='order' AND payment_status='succeeded';

CREATE INDEX IF NOT EXISTS idx_merchant_ledger_settlement
  ON merchant_ledger(settlement_status, created_at);
