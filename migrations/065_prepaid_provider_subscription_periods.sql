ALTER TABLE provider_subscriptions ADD COLUMN duration_months INTEGER NOT NULL DEFAULT 1
  CHECK (duration_months IN (1,3,6,12));
ALTER TABLE provider_subscriptions ADD CONSTRAINT provider_recurring_monthly_only
  CHECK (billing_mode <> 'recurring' OR duration_months = 1);

CREATE FUNCTION protect_provider_subscription_purchase() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.duration_months IS DISTINCT FROM OLD.duration_months
    OR NEW.billing_mode IS DISTINCT FROM OLD.billing_mode
    OR NEW.expected_amount_ngn IS DISTINCT FROM OLD.expected_amount_ngn THEN
    RAISE EXCEPTION 'subscription purchase amount and period are immutable';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER provider_subscription_purchase_immutable BEFORE UPDATE ON provider_subscriptions
FOR EACH ROW EXECUTE FUNCTION protect_provider_subscription_purchase();
