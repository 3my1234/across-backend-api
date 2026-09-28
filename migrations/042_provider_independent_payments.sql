CREATE TABLE IF NOT EXISTS payment_method_policies (
  country_code CHAR(2) NOT NULL,
  currency_code CHAR(3) NOT NULL,
  provider TEXT NOT NULL,
  payment_methods TEXT[] NOT NULL DEFAULT ARRAY['card'],
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (country_code, currency_code, provider),
  CHECK (provider = lower(provider)),
  CHECK (cardinality(payment_methods) > 0)
);

INSERT INTO payment_method_policies(country_code, currency_code, provider, payment_methods)
SELECT country_code, currency_code, 'flutterwave',
  CASE country_code
    WHEN 'NG' THEN ARRAY['card','banktransfer','ussd']
    WHEN 'GH' THEN ARRAY['card','mobilemoneyghana']
    WHEN 'KE' THEN ARRAY['card','mpesa']
    WHEN 'UG' THEN ARRAY['card','mobilemoneyuganda']
    WHEN 'TZ' THEN ARRAY['card','mobilemoneytanzania']
    WHEN 'RW' THEN ARRAY['card','mobilemoneyrwanda']
    WHEN 'ZM' THEN ARRAY['card','mobilemoneyzambia']
    ELSE ARRAY['card']
  END
FROM countries_config
WHERE 'flutterwave' = ANY(active_payment_gateways)
ON CONFLICT (country_code, currency_code, provider) DO NOTHING;

CREATE TABLE IF NOT EXISTS payments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider TEXT NOT NULL,
  purpose TEXT NOT NULL CHECK (purpose IN ('order','provider_subscription')),
  order_id UUID REFERENCES orders(id) ON DELETE RESTRICT,
  provider_subscription_id UUID REFERENCES provider_subscriptions(id) ON DELETE RESTRICT,
  user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
  country_code CHAR(2) NOT NULL,
  amount NUMERIC(14,2) NOT NULL CHECK (amount > 0),
  currency_code CHAR(3) NOT NULL,
  provider_reference TEXT NOT NULL,
  provider_transaction_id TEXT,
  idempotency_key TEXT NOT NULL,
  payment_method TEXT NOT NULL DEFAULT '',
  payment_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (payment_status IN ('pending','processing','succeeded','failed','cancelled')),
  refund_status TEXT NOT NULL DEFAULT 'none'
    CHECK (refund_status IN ('none','pending','partial','refunded','failed')),
  chargeback_status TEXT NOT NULL DEFAULT 'none'
    CHECK (chargeback_status IN ('none','open','won','lost','accepted')),
  settlement_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (settlement_status IN ('pending','settled','failed','reversed')),
  provider_status TEXT NOT NULL DEFAULT '',
  failure_code TEXT NOT NULL DEFAULT '',
  failure_message TEXT NOT NULL DEFAULT '',
  checkout_url TEXT NOT NULL DEFAULT '',
  paid_at TIMESTAMPTZ,
  settled_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (provider = lower(provider)),
  CHECK (currency_code = upper(currency_code)),
  CHECK (country_code = upper(country_code)),
  CHECK (
    (purpose = 'order' AND order_id IS NOT NULL AND provider_subscription_id IS NULL) OR
    (purpose = 'provider_subscription' AND order_id IS NULL AND provider_subscription_id IS NOT NULL)
  ),
  UNIQUE(provider, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_payments_provider_reference
  ON payments(provider, provider_reference, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_payments_provider_transaction
  ON payments(provider, provider_transaction_id)
  WHERE provider_transaction_id IS NOT NULL AND provider_transaction_id <> '';
CREATE INDEX IF NOT EXISTS idx_payments_order_history ON payments(order_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_payments_subscription_history ON payments(provider_subscription_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_payments_lifecycle ON payments(payment_status, refund_status, chargeback_status, settlement_status, created_at DESC);

CREATE OR REPLACE FUNCTION protect_payment_financial_identity()
RETURNS trigger AS $$
BEGIN
  IF NEW.provider IS DISTINCT FROM OLD.provider
    OR NEW.purpose IS DISTINCT FROM OLD.purpose
    OR NEW.order_id IS DISTINCT FROM OLD.order_id
    OR NEW.provider_subscription_id IS DISTINCT FROM OLD.provider_subscription_id
    OR NEW.user_id IS DISTINCT FROM OLD.user_id
    OR NEW.country_code IS DISTINCT FROM OLD.country_code
    OR NEW.amount IS DISTINCT FROM OLD.amount
    OR NEW.currency_code IS DISTINCT FROM OLD.currency_code
    OR NEW.provider_reference IS DISTINCT FROM OLD.provider_reference
    OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key THEN
    RAISE EXCEPTION 'payment financial identity is immutable';
  END IF;
  IF OLD.provider_transaction_id IS NOT NULL
    AND NEW.provider_transaction_id IS DISTINCT FROM OLD.provider_transaction_id THEN
    RAISE EXCEPTION 'payment provider transaction ID is immutable once assigned';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_protect_payment_financial_identity ON payments;
CREATE TRIGGER trg_protect_payment_financial_identity
BEFORE UPDATE ON payments
FOR EACH ROW EXECUTE FUNCTION protect_payment_financial_identity();

CREATE TABLE IF NOT EXISTS payment_webhook_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider TEXT NOT NULL,
  event_key TEXT NOT NULL,
  event_type TEXT NOT NULL DEFAULT '',
  provider_reference TEXT NOT NULL DEFAULT '',
  provider_transaction_id TEXT NOT NULL DEFAULT '',
  payload_sha256 TEXT NOT NULL,
  payload JSONB NOT NULL,
  processing_status TEXT NOT NULL DEFAULT 'processing'
    CHECK (processing_status IN ('processing','processed','ignored','failed')),
  attempt_count INTEGER NOT NULL DEFAULT 1 CHECK (attempt_count > 0),
  last_error TEXT NOT NULL DEFAULT '',
  first_received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  processed_at TIMESTAMPTZ,
  UNIQUE(provider, event_key)
);
CREATE INDEX IF NOT EXISTS idx_payment_webhooks_status ON payment_webhook_events(processing_status, last_received_at);

INSERT INTO payments(
  provider,purpose,order_id,user_id,country_code,amount,currency_code,
  provider_reference,provider_transaction_id,idempotency_key,
  payment_status,settlement_status,provider_status,paid_at,settled_at,created_at,updated_at
)
SELECT 'flutterwave','order',o.id,o.user_id,c.country_code,o.total_amount,o.currency_code,
  o.flutterwave_tx_ref,NULLIF(o.flutterwave_transaction_id,''),
  'legacy-order:' || o.id::text || ':' || o.flutterwave_tx_ref,
  CASE WHEN o.paid_at IS NOT NULL THEN 'succeeded' ELSE 'processing' END,
  'pending',
  CASE WHEN o.paid_at IS NOT NULL THEN 'successful' ELSE '' END,
  o.paid_at,NULL,o.created_at,o.updated_at
FROM orders o
JOIN countries_config c ON c.id=o.country_id
WHERE o.flutterwave_tx_ref IS NOT NULL AND o.flutterwave_tx_ref <> ''
ON CONFLICT (provider, idempotency_key) DO NOTHING;

INSERT INTO payments(
  provider,purpose,provider_subscription_id,user_id,country_code,amount,currency_code,
  provider_reference,provider_transaction_id,idempotency_key,
  payment_status,settlement_status,provider_status,paid_at,settled_at,created_at,updated_at
)
SELECT 'flutterwave','provider_subscription',sp.subscription_id,po.owner_user_id,po.country_code,
  sp.amount,sp.currency_code,sp.tx_ref,sp.flutterwave_transaction_id,
  'legacy-subscription:' || sp.id::text,
  'succeeded','pending','successful',sp.paid_at,NULL,sp.paid_at,sp.paid_at
FROM provider_subscription_payments sp
JOIN provider_subscriptions ps ON ps.id=sp.subscription_id
JOIN provider_organizations po ON po.id=ps.provider_id
ON CONFLICT (provider, idempotency_key) DO NOTHING;
