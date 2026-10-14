-- Preserve all paid orders, payment ledgers, payout history and existing moderation.
ALTER TABLE provider_subscription_plans ADD COLUMN IF NOT EXISTS badge_tier TEXT NOT NULL DEFAULT ''
 CHECK (badge_tier IN ('','bronze','silver','gold','diamond'));
UPDATE provider_subscription_plans SET badge_tier=CASE code
 WHEN 'provider-monthly' THEN 'bronze'
 WHEN 'provider-growth' THEN 'silver'
 WHEN 'provider-business' THEN 'gold'
 WHEN 'provider-diamond' THEN 'diamond' ELSE badge_tier END
WHERE code IN ('provider-monthly','provider-growth','provider-business','provider-diamond');

CREATE OR REPLACE VIEW provider_public_identity AS
SELECT p.id AS provider_id,p.business_name AS name,COALESCE(NULLIF(p.logo_url,''),(SELECT u.avatar_url FROM users u WHERE u.id=p.owner_user_id),'') AS avatar_url,
 (p.is_active AND p.verification_status='approved') AS verified,
 COALESCE(s.paid_active,false) AS paid_active,
 CASE WHEN p.is_active AND p.verification_status='approved' THEN COALESCE(s.badge_tier,'') ELSE '' END AS badge_tier
FROM provider_organizations p
LEFT JOIN LATERAL (
 SELECT true AS paid_active,plan.badge_tier
 FROM provider_subscriptions sub JOIN provider_subscription_plans plan ON plan.id=sub.plan_id
 WHERE sub.provider_id=p.id AND sub.status='active' AND sub.current_period_end>now()
 AND (sub.starts_at IS NULL OR sub.starts_at<=now())
 AND EXISTS(SELECT 1 FROM provider_subscription_payments pay WHERE pay.subscription_id=sub.id
   AND pay.currency_code='NGN' AND pay.amount>0)
 ORDER BY sub.current_period_end DESC,sub.created_at DESC,sub.id DESC LIMIT 1
) s ON true;

UPDATE products SET payment_mode='contact',catalog_version=catalog_version+1,updated_at=now()
WHERE provider_id IS NOT NULL AND payment_mode<>'contact';
ALTER TABLE products ALTER COLUMN payment_mode SET DEFAULT 'contact';
CREATE OR REPLACE FUNCTION enforce_provider_product_contact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.provider_id IS NOT NULL AND NEW.payment_mode<>'contact' THEN
   RAISE EXCEPTION 'Products use seller messaging; Flutterwave is reserved for provider subscriptions';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS provider_product_contact_only ON products;
CREATE TRIGGER provider_product_contact_only BEFORE INSERT OR UPDATE OF payment_mode,provider_id ON products
FOR EACH ROW EXECUTE FUNCTION enforce_provider_product_contact();
