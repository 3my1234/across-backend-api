-- Renewal preferences never change an entitlement that has already been paid for.
ALTER TABLE provider_subscriptions ADD COLUMN gateway_plan_id BIGINT;
UPDATE provider_subscriptions s SET gateway_plan_id=p.flutterwave_plan_id
FROM provider_subscription_plans p WHERE p.id=s.plan_id AND s.billing_mode='recurring';
CREATE FUNCTION protect_provider_gateway_plan() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.gateway_plan_id IS DISTINCT FROM OLD.gateway_plan_id THEN
    RAISE EXCEPTION 'subscription gateway plan is immutable';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER provider_gateway_plan_immutable BEFORE UPDATE ON provider_subscriptions
FOR EACH ROW EXECUTE FUNCTION protect_provider_gateway_plan();

CREATE TABLE provider_renewal_preferences (
  provider_id UUID PRIMARY KEY REFERENCES provider_organizations(id) ON DELETE CASCADE,
  renewal_status TEXT NOT NULL CHECK (renewal_status IN ('stopped','uncertain')),
  next_plan_id UUID REFERENCES provider_subscription_plans(id) ON DELETE RESTRICT,
  change_at TIMESTAMPTZ,
  updated_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((next_plan_id IS NULL) = (change_at IS NULL))
);
CREATE TABLE provider_renewal_cancellations (
  gateway_subscription_id BIGINT PRIMARY KEY,
  provider_id UUID NOT NULL REFERENCES provider_organizations(id) ON DELETE RESTRICT,
  requested_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK (status IN ('requested','confirmed','uncertain')),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
