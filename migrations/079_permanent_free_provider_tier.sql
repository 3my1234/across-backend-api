-- Permanent Free access replaces launch dates. No synthetic paid subscription is created.
-- Existing listings are grandfathered; their moderation/visibility is not changed here.
CREATE OR REPLACE VIEW provider_public_identity AS
SELECT p.id AS provider_id,p.business_name AS name,COALESCE(NULLIF(p.logo_url,''),(SELECT u.avatar_url FROM users u WHERE u.id=p.owner_user_id),'') AS avatar_url,
 (p.is_active AND p.verification_status='approved') AS verified,
 COALESCE(s.paid_active,false) AS paid_active,
 CASE WHEN p.is_active AND p.verification_status='approved' THEN COALESCE(s.badge_tier,'') ELSE '' END AS badge_tier,
 COALESCE(s.listing_limit,1) AS listing_limit
FROM provider_organizations p
LEFT JOIN LATERAL (
 SELECT true AS paid_active,plan.badge_tier,plan.listing_limit
 FROM provider_subscriptions sub JOIN provider_subscription_plans plan ON plan.id=sub.plan_id
 WHERE sub.provider_id=p.id AND sub.status='active' AND sub.current_period_end>now()
 AND (sub.starts_at IS NULL OR sub.starts_at<=now())
 AND EXISTS(SELECT 1 FROM provider_subscription_payments pay WHERE pay.subscription_id=sub.id AND pay.currency_code='NGN' AND pay.amount>0)
 ORDER BY sub.current_period_end DESC,sub.created_at DESC,sub.id DESC LIMIT 1
) s ON true;

CREATE OR REPLACE FUNCTION enforce_provider_listing_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cap integer; paid boolean; used integer; provider uuid;
BEGIN
 provider := NEW.provider_id;
 IF provider IS NULL THEN RETURN NEW; END IF;
 -- Serialize across both tables, preventing concurrent product/service bypasses.
 PERFORM 1 FROM provider_organizations WHERE id=provider FOR UPDATE;
 SELECT paid_active,listing_limit INTO paid,cap FROM provider_public_identity WHERE provider_id=provider;
 IF cap IS NULL THEN RAISE EXCEPTION 'Provider profile is required'; END IF;
 IF TG_OP='INSERT' THEN
   IF NOT paid THEN
     SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status<>'archived')+
            (SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status<>'archived') INTO used;
   ELSIF TG_TABLE_NAME='products' THEN
     SELECT count(*) INTO used FROM products WHERE provider_id=provider AND moderation_status<>'archived';
   ELSE
     SELECT count(*) INTO used FROM provider_listings WHERE provider_id=provider AND status<>'archived';
   END IF;
   IF used>=cap THEN RAISE EXCEPTION 'Your plan listing limit has been reached. Free allows one listing total; choose a paid plan for more listings.' USING ERRCODE='23514'; END IF;
 ELSIF NOT paid AND OLD.published_at IS NULL AND NEW.published_at IS NOT NULL THEN
   SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status='approved' AND id<>NEW.id)+
          (SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status='approved' AND id<>NEW.id) INTO used;
   IF used>=1 THEN RAISE EXCEPTION 'Free allows one published listing total. Choose a paid plan for more listings.' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS provider_product_capacity ON products;
CREATE TRIGGER provider_product_capacity BEFORE INSERT OR UPDATE OF published_at ON products FOR EACH ROW EXECUTE FUNCTION enforce_provider_listing_capacity();
DROP TRIGGER IF EXISTS provider_service_capacity ON provider_listings;
CREATE TRIGGER provider_service_capacity BEFORE INSERT OR UPDATE OF published_at ON provider_listings FOR EACH ROW EXECUTE FUNCTION enforce_provider_listing_capacity();
