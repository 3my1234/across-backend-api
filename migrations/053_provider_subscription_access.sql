-- Persist the provider free/paid switch for all API replicas. NULL preserves
-- the existing environment configuration until a super-admin saves a choice.
CREATE TABLE IF NOT EXISTS provider_subscription_access (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  enforced BOOLEAN,
  start_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by UUID REFERENCES admins(id) ON DELETE SET NULL,
  CHECK (enforced IS DISTINCT FROM FALSE OR start_at IS NULL)
);
INSERT INTO provider_subscription_access(singleton, enforced) VALUES(TRUE, NULL)
ON CONFLICT(singleton) DO NOTHING;

CREATE TABLE IF NOT EXISTS provider_subscription_access_events (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  admin_id UUID NOT NULL REFERENCES admins(id) ON DELETE RESTRICT,
  old_enforced BOOLEAN,
  old_start_at TIMESTAMPTZ,
  new_enforced BOOLEAN NOT NULL,
  new_start_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS provider_subscription_plan_price_events (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  plan_id UUID NOT NULL REFERENCES provider_subscription_plans(id) ON DELETE RESTRICT,
  admin_id UUID NOT NULL REFERENCES admins(id) ON DELETE RESTRICT,
  old_amount_ngn NUMERIC(14,2) NOT NULL,
  new_amount_ngn NUMERIC(14,2) NOT NULL,
  old_flutterwave_plan_id BIGINT,
  new_flutterwave_plan_id BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Keep each checkout's agreed amount stable if an admin changes the plan later.
ALTER TABLE provider_subscriptions ADD COLUMN IF NOT EXISTS expected_amount_ngn NUMERIC(14,2);
UPDATE provider_subscriptions s SET expected_amount_ngn = COALESCE(
  (SELECT p.amount FROM provider_subscription_payments p WHERE p.subscription_id=s.id ORDER BY p.paid_at ASC LIMIT 1),
  (SELECT p.amount_ngn FROM provider_subscription_plans p WHERE p.id=s.plan_id)
) WHERE expected_amount_ngn IS NULL;
ALTER TABLE provider_subscriptions ALTER COLUMN expected_amount_ngn SET NOT NULL;
ALTER TABLE provider_subscriptions ADD CONSTRAINT provider_subscriptions_expected_amount_positive CHECK (expected_amount_ngn > 0);

CREATE TABLE IF NOT EXISTS provider_gateway_subscription_cancellations (
  subscription_id BIGINT PRIMARY KEY,
  plan_id BIGINT NOT NULL,
  admin_id UUID NOT NULL REFERENCES admins(id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK (status IN ('pending','confirmed','uncertain')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
