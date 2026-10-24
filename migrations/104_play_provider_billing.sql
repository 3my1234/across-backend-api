-- Additive store ledger. Existing Flutterwave IDs remain real gateway IDs;
-- Google orders have their own unique column and are never impersonated as one.
ALTER TABLE provider_subscriptions ADD COLUMN billing_provider TEXT NOT NULL DEFAULT 'flutterwave'
 CHECK(billing_provider IN ('flutterwave','google_play'));
ALTER TABLE provider_subscription_payments ALTER COLUMN flutterwave_transaction_id DROP NOT NULL;
ALTER TABLE provider_subscription_payments ADD COLUMN billing_provider TEXT NOT NULL DEFAULT 'flutterwave'
 CHECK(billing_provider IN ('flutterwave','google_play'));
ALTER TABLE provider_subscription_payments ADD COLUMN play_order_id TEXT UNIQUE;
ALTER TABLE provider_subscription_payments ADD COLUMN test_purchase BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE provider_subscription_payments ADD COLUMN referral_eligible BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE provider_subscription_payments ADD CONSTRAINT provider_receipt_origin CHECK (
 (billing_provider='flutterwave' AND flutterwave_transaction_id IS NOT NULL AND play_order_id IS NULL AND NOT test_purchase)
 OR (billing_provider='google_play' AND flutterwave_transaction_id IS NULL AND play_order_id LIKE 'GPA.%'));

CREATE TABLE play_provider_products (
 product_id TEXT NOT NULL CHECK(product_id ~ '^[a-z0-9][a-z0-9._-]{0,199}$'),
 base_plan_id TEXT NOT NULL CHECK(base_plan_id ~ '^[a-z0-9][a-z0-9._-]{0,199}$'),
 plan_id UUID NOT NULL REFERENCES provider_subscription_plans(id),
 duration_months INTEGER NOT NULL CHECK(duration_months IN(1,12)),
 amount_minor BIGINT NOT NULL CHECK(amount_minor>0 AND amount_minor<100000000000000),
 enabled BOOLEAN NOT NULL DEFAULT false,
 PRIMARY KEY(product_id,base_plan_id), UNIQUE(plan_id,duration_months)
);
-- Mapping is deliberately empty until real products/prices are independently
-- checked in Play Console. No guessed SKU or synthetic purchase is enabled.
CREATE TABLE play_provider_purchases (
 token_hash TEXT PRIMARY KEY CHECK(token_hash ~ '^[a-f0-9]{64}$'),
 provider_id UUID NOT NULL REFERENCES provider_organizations(id),
 subscription_id UUID NOT NULL UNIQUE REFERENCES provider_subscriptions(id),
 product_id TEXT NOT NULL,
 base_plan_id TEXT NOT NULL,
 encrypted_token BYTEA NOT NULL CHECK(octet_length(encrypted_token) BETWEEN 44 AND 8192),
 state TEXT NOT NULL,
 auto_renews BOOLEAN NOT NULL DEFAULT false,
 test_purchase BOOLEAN NOT NULL DEFAULT false,
 needs_acknowledgement BOOLEAN NOT NULL DEFAULT false,
 checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(product_id,base_plan_id) REFERENCES play_provider_products(product_id,base_plan_id)
);
CREATE INDEX play_provider_reconcile ON play_provider_purchases(next_check_at);
CREATE TABLE play_provider_refund_jobs (
 token_hash TEXT PRIMARY KEY CHECK(token_hash ~ '^[a-f0-9]{64}$'),
 provider_id UUID NOT NULL REFERENCES provider_organizations(id),
 product_id TEXT NOT NULL, base_plan_id TEXT NOT NULL,
 encrypted_token BYTEA NOT NULL CHECK(octet_length(encrypted_token) BETWEEN 44 AND 8192),
 status TEXT NOT NULL DEFAULT 'requested' CHECK(status IN('requested','confirmed')),
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(product_id,base_plan_id) REFERENCES play_provider_products(product_id,base_plan_id)
);
CREATE INDEX play_provider_refund_due ON play_provider_refund_jobs(next_attempt_at) WHERE status='requested';
CREATE TABLE play_provider_checkout_intents (
 provider_id UUID PRIMARY KEY REFERENCES provider_organizations(id),
 product_id TEXT NOT NULL,
 base_plan_id TEXT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 FOREIGN KEY(product_id,base_plan_id) REFERENCES play_provider_products(product_id,base_plan_id)
);
CREATE FUNCTION protect_play_product_mapping() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.plan_id,NEW.product_id,NEW.base_plan_id,NEW.duration_months,NEW.amount_minor)
  IS DISTINCT FROM (OLD.plan_id,OLD.product_id,OLD.base_plan_id,OLD.duration_months,OLD.amount_minor)
  AND EXISTS(SELECT 1 FROM play_provider_purchases WHERE product_id=OLD.product_id AND base_plan_id=OLD.base_plan_id)
 THEN RAISE EXCEPTION 'Purchased Play product mappings are immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER immutable_play_product_mapping BEFORE UPDATE ON play_provider_products
 FOR EACH ROW EXECUTE FUNCTION protect_play_product_mapping();

-- Store license-test purchases never create cash-equivalent referral rewards.
CREATE OR REPLACE FUNCTION reconcile_referral_reward(referred UUID) RETURNS void LANGUAGE plpgsql AS $$
DECLARE referral user_referrals%ROWTYPE; eligible BOOLEAN; awarded INTEGER;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM referral_program_settings WHERE id AND enabled) THEN RETURN; END IF;
 SELECT * INTO referral FROM user_referrals WHERE referred_user_id=referred FOR UPDATE;
 IF NOT FOUND THEN RETURN; END IF;
 SELECT EXISTS(SELECT 1 FROM users a JOIN users b ON b.id=referral.referrer_user_id
               WHERE a.id=referred AND a.email_verified AND a.is_active AND b.email_verified AND b.is_active) INTO eligible;
 IF NOT eligible THEN RETURN; END IF;
 IF referral.signup_awarded_at IS NULL THEN
  INSERT INTO xp_transactions(user_id,amount,reason,reference_id)
   VALUES(referral.referrer_user_id,5,'referral_signup',referred::text) ON CONFLICT DO NOTHING;
  GET DIAGNOSTICS awarded=ROW_COUNT;
  IF awarded=1 THEN
   INSERT INTO notifications(user_id,type,title,body,data,event_key)
    VALUES(referral.referrer_user_id,'xp_earned','Referral signup reward','You earned 5 XP for a verified new account.',
           jsonb_build_object('xp',5,'reason','referral_signup'),'referral-signup:'||referred::text) ON CONFLICT DO NOTHING;
  END IF;
  UPDATE user_referrals SET signup_awarded_at=now() WHERE referred_user_id=referred;
 END IF;
 IF referral.provider_awarded_at IS NULL THEN
  SELECT EXISTS(
   SELECT 1 FROM provider_organizations org
   JOIN provider_subscriptions sub ON sub.provider_id=org.id
   JOIN provider_subscription_plans plan ON plan.id=sub.plan_id
   JOIN provider_subscription_payments pay ON pay.subscription_id=sub.id
   WHERE org.owner_user_id=referred AND pay.currency_code='NGN' AND pay.amount>0 AND NOT pay.test_purchase AND pay.referral_eligible
    AND sub.expected_amount_ngn>0 AND pay.amount+0.01>=sub.expected_amount_ngn
    AND plan.code IN ('provider-monthly','provider-growth','provider-business','provider-diamond')
    AND plan.amount_ngn IN (500,1000,2000,5000)
  ) INTO eligible;
  IF eligible THEN
   INSERT INTO xp_transactions(user_id,amount,reason,reference_id)
    VALUES(referral.referrer_user_id,495,'referral_provider',referred::text) ON CONFLICT DO NOTHING;
   GET DIAGNOSTICS awarded=ROW_COUNT;
   IF awarded=1 THEN
    INSERT INTO notifications(user_id,type,title,body,data,event_key)
     VALUES(referral.referrer_user_id,'xp_earned','Provider referral reward','A referred provider made their first qualifying payment. You earned another 495 XP: 500 XP total for this referral.',
            jsonb_build_object('xp',495,'reason','referral_provider'),'referral-provider:'||referred::text) ON CONFLICT DO NOTHING;
   END IF;
   UPDATE user_referrals SET provider_awarded_at=now() WHERE referred_user_id=referred;
  END IF;
 END IF;
END $$;
