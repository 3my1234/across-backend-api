-- Product payments are marketplace split payments. Atlantic Express receives
-- a 1% service fee and Flutterwave settles the remainder to the seller's
-- verified collection subaccount.

ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS platform_fee NUMERIC(14,2) NOT NULL DEFAULT 0
    CHECK (platform_fee >= 0);

CREATE TABLE IF NOT EXISTS provider_payout_accounts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id UUID NOT NULL UNIQUE REFERENCES provider_organizations(id) ON DELETE CASCADE,
  payment_provider TEXT NOT NULL DEFAULT 'flutterwave'
    CHECK (payment_provider IN ('flutterwave')),
  country_code CHAR(2) NOT NULL,
  currency_code CHAR(3) NOT NULL,
  account_bank TEXT NOT NULL,
  account_number_last4 TEXT NOT NULL,
  account_name TEXT NOT NULL,
  bank_name TEXT NOT NULL,
  flutterwave_subaccount_id TEXT NOT NULL UNIQUE,
  flutterwave_subaccount_numeric_id BIGINT,
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('pending','active','disabled','failed')),
  failure_message TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_provider_payout_accounts_status
  ON provider_payout_accounts(provider_id, status);

-- Remove obsolete estimated import/VAT charges from unpaid test orders.
WITH item_totals AS (
  SELECT order_id, ROUND(SUM(unit_price * quantity)::numeric, 2) AS subtotal
  FROM order_items
  GROUP BY order_id
)
UPDATE orders o
SET customs_fee = 0,
    vat_fee = 0,
    stamp_duty_fee = 0,
    platform_fee = ROUND(item_totals.subtotal * 0.01, 2),
    total_amount = ROUND(item_totals.subtotal * 1.01, 2),
    updated_at = now()
FROM item_totals
WHERE o.id = item_totals.order_id
  AND o.order_status = 'Pending'
  AND o.paid_at IS NULL;
