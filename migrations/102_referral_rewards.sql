CREATE TABLE referral_program_settings (
 id BOOLEAN PRIMARY KEY DEFAULT true CHECK(id),
 enabled BOOLEAN NOT NULL DEFAULT true
);
INSERT INTO referral_program_settings(id,enabled) VALUES(true,true);
CREATE TABLE referral_codes (
 user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 code TEXT NOT NULL UNIQUE CHECK (code ~ '^[A-Z0-9]{12}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE user_referrals (
 referred_user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 referrer_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 signup_awarded_at TIMESTAMPTZ,
 provider_awarded_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (referred_user_id <> referrer_user_id)
);
CREATE INDEX user_referrals_referrer ON user_referrals(referrer_user_id, created_at DESC);

-- The existing signed XP ledger and admin-reviewed withdrawals remain the only
-- balance/payout system. A qualifying provider earns 500 TOTAL, not 5 + 500.
CREATE FUNCTION reconcile_referral_reward(referred UUID) RETURNS void LANGUAGE plpgsql AS $$
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
   WHERE org.owner_user_id=referred AND pay.currency_code='NGN' AND pay.amount>0
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
CREATE FUNCTION referral_verified_user() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.email_verified AND NEW.is_active THEN PERFORM reconcile_referral_reward(NEW.id); END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER referral_user_verified AFTER UPDATE OF email_verified,is_active ON users
 FOR EACH ROW EXECUTE FUNCTION referral_verified_user();
CREATE FUNCTION referral_attached() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM reconcile_referral_reward(NEW.referred_user_id); RETURN NEW; END $$;
CREATE TRIGGER referral_after_attach AFTER INSERT ON user_referrals FOR EACH ROW EXECUTE FUNCTION referral_attached();
CREATE FUNCTION referral_paid_provider() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner_id UUID;
BEGIN
 SELECT org.owner_user_id INTO owner_id FROM provider_subscriptions sub JOIN provider_organizations org ON org.id=sub.provider_id WHERE sub.id=NEW.subscription_id;
 IF owner_id IS NOT NULL THEN PERFORM reconcile_referral_reward(owner_id); END IF;
 RETURN NEW;
END $$;
-- This table is populated only by the server's verified settlement path. Browser
-- checkout returns, a plan selection and subscription status alone award no XP.
CREATE TRIGGER referral_payment_confirmed AFTER INSERT ON provider_subscription_payments
 FOR EACH ROW EXECUTE FUNCTION referral_paid_provider();
