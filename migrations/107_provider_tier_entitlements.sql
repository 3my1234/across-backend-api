-- Explicit tier benefits; keep IDs, codes, prices, payment links and historical periods.
CREATE FUNCTION provider_tier_benefits(tier text) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
SELECT jsonb_build_object('post_limit',CASE tier WHEN 'bronze' THEN 3 WHEN 'silver' THEN 10 WHEN 'diamond' THEN 25 WHEN 'gold' THEN 60 ELSE 1 END,
 'max_images',CASE tier WHEN 'bronze' THEN 3 WHEN 'silver' THEN 5 WHEN 'diamond' THEN 8 WHEN 'gold' THEN 12 ELSE 1 END,
 'max_videos',CASE tier WHEN 'diamond' THEN 2 WHEN 'gold' THEN 5 ELSE 0 END,'max_video_seconds',60,
 'town_hall',tier IN('bronze','silver','diamond','gold'),
 'town_hall_room_limit',CASE tier WHEN 'bronze' THEN 3 WHEN 'silver' THEN 5 WHEN 'diamond' THEN 8 WHEN 'gold' THEN 10 ELSE 0 END,
 'promotional_weight',CASE tier WHEN 'silver' THEN 2 WHEN 'diamond' THEN 3 WHEN 'gold' THEN 5 ELSE 1 END,
 'visibility',CASE tier WHEN 'bronze' THEN 'basic' WHEN 'silver' THEN 'standard' WHEN 'diamond' THEN 'enhanced' WHEN 'gold' THEN 'premium' ELSE 'limited' END,
 'combined_limit',true,'direct_messaging',true,'recommended',true,'town_hall_search',tier IN('bronze','silver','diamond','gold'));
$$;
UPDATE provider_subscription_plans SET
 name=CASE code WHEN 'provider-monthly' THEN 'Bronze' WHEN 'provider-growth' THEN 'Silver' WHEN 'provider-business' THEN 'Diamond' WHEN 'provider-diamond' THEN 'Gold' END,
 badge_tier=CASE code WHEN 'provider-monthly' THEN 'bronze' WHEN 'provider-growth' THEN 'silver' WHEN 'provider-business' THEN 'diamond' WHEN 'provider-diamond' THEN 'gold' END,
 listing_limit=CASE code WHEN 'provider-monthly' THEN 3 WHEN 'provider-growth' THEN 10 WHEN 'provider-business' THEN 25 WHEN 'provider-diamond' THEN 60 END,
 description=CASE code WHEN 'provider-monthly' THEN 'Start selling and connecting with customers.' WHEN 'provider-growth' THEN 'Grow your business with more listings and visibility.' WHEN 'provider-business' THEN 'Reach more potential customers and manage more opportunities.' WHEN 'provider-diamond' THEN 'Maximum listing capacity and premium promotional opportunities.' END,
 updated_at=now()
WHERE code IN('provider-monthly','provider-growth','provider-business','provider-diamond');

-- Existing active paid publications retain visibility when a quota decreases.
CREATE OR REPLACE VIEW provider_public_identity AS
SELECT p.id provider_id,p.business_name name,COALESCE(NULLIF(p.logo_url,''),(SELECT u.avatar_url FROM users u WHERE u.id=p.owner_user_id),'') avatar_url,
 (p.is_active AND p.verification_status='approved') verified,COALESCE(s.paid_active,false) paid_active,
 CASE WHEN p.is_active AND p.verification_status='approved' THEN COALESCE(s.badge_tier,'') ELSE '' END badge_tier,
 CASE WHEN s.paid_active THEN GREATEST(s.listing_limit,COALESCE((SELECT max(slot) FROM (
 SELECT plan_slot slot FROM products WHERE provider_id=p.id AND moderation_status='approved' AND published_at IS NOT NULL
 UNION ALL SELECT plan_slot FROM provider_listings WHERE provider_id=p.id AND status='approved' AND published_at IS NOT NULL) existing),1)) ELSE 1 END::int listing_limit
FROM provider_organizations p LEFT JOIN LATERAL (
 SELECT true paid_active,plan.badge_tier,plan.listing_limit FROM provider_subscriptions sub JOIN provider_subscription_plans plan ON plan.id=sub.plan_id
 WHERE sub.provider_id=p.id AND sub.status='active' AND sub.current_period_end>now() AND(sub.starts_at IS NULL OR sub.starts_at<=now())
 AND EXISTS(SELECT 1 FROM provider_subscription_payments pay WHERE pay.subscription_id=sub.id AND pay.currency_code='NGN' AND pay.amount>0)
 ORDER BY sub.current_period_end DESC,sub.created_at DESC,sub.id DESC LIMIT 1
) s ON true;

-- Current verified receipts/periods are resolved by the existing identity view.
-- Preserve configured capacity for older custom paid tiers outside the four named plans.
CREATE FUNCTION provider_current_benefits(provider uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT provider_tier_benefits(i.badge_tier) || CASE WHEN i.paid_active AND i.badge_tier='' THEN jsonb_build_object('post_limit',COALESCE((
 SELECT plan.listing_limit FROM provider_subscriptions sub JOIN provider_subscription_plans plan ON plan.id=sub.plan_id
 WHERE sub.provider_id=provider AND sub.status='active' AND sub.current_period_end>now() AND(sub.starts_at IS NULL OR sub.starts_at<=now())
 AND EXISTS(SELECT 1 FROM provider_subscription_payments pay WHERE pay.subscription_id=sub.id AND pay.currency_code='NGN' AND pay.amount>0)
 ORDER BY sub.current_period_end DESC,sub.created_at DESC,sub.id DESC LIMIT 1),1)) ELSE '{}'::jsonb END
 FROM provider_public_identity i WHERE i.provider_id=provider;
$$;

CREATE OR REPLACE FUNCTION enforce_provider_listing_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cap integer; used integer; provider uuid; tier text; max_images integer; max_videos integer; images jsonb; videos jsonb; item jsonb; previous jsonb; row_data jsonb;
BEGIN
 provider:=NEW.provider_id;IF provider IS NULL THEN RETURN NEW;END IF;
 IF TG_OP='UPDATE' AND NEW.provider_id IS DISTINCT FROM OLD.provider_id THEN RAISE EXCEPTION 'Your plan listing owner cannot be changed.' USING ERRCODE='23514';END IF;
 PERFORM 1 FROM provider_organizations WHERE id=provider FOR UPDATE;
 SELECT badge_tier INTO tier FROM provider_public_identity WHERE provider_id=provider;
 cap:=(provider_current_benefits(provider)->>'post_limit')::int;
 IF cap IS NULL THEN RAISE EXCEPTION 'Provider profile is required';END IF;
 row_data:=to_jsonb(NEW);previous:=CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
 IF TG_OP='INSERT' OR (COALESCE(previous->>'status',previous->>'moderation_status')='archived' AND COALESCE(row_data->>'status',row_data->>'moderation_status')<>'archived') THEN
  SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status<>'archived' AND id<>NEW.id)+(SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status<>'archived' AND id<>NEW.id) INTO used;
  IF used>=cap THEN RAISE EXCEPTION 'Your plan post limit has been reached. Products and services share one limit. Archive a post or upgrade.' USING ERRCODE='23514';END IF;
 END IF;
 max_images:=(provider_tier_benefits(tier)->>'max_images')::int;
 max_videos:=(provider_tier_benefits(tier)->>'max_videos')::int;
 images:=COALESCE(row_data->'image_urls',row_data->'media_urls','[]'::jsonb);
 videos:=COALESCE(row_data#>'{attributes,video_urls}',row_data#>'{factory_details,video_urls}','[]'::jsonb);
 -- Preserve existing records. New media or resubmission must meet today's plan.
 IF TG_OP='INSERT' OR row_data->'image_urls' IS DISTINCT FROM previous->'image_urls' OR row_data->'media_urls' IS DISTINCT FROM previous->'media_urls' OR row_data#>'{attributes,video_urls}' IS DISTINCT FROM previous#>'{attributes,video_urls}' OR row_data#>'{factory_details,video_urls}' IS DISTINCT FROM previous#>'{factory_details,video_urls}' THEN
  IF jsonb_typeof(images)<>'array' OR (jsonb_array_length(images)>max_images AND (TG_OP='INSERT' OR NOT(images <@ COALESCE(previous->'image_urls',previous->'media_urls','[]'::jsonb)) OR jsonb_array_length(images)>jsonb_array_length(COALESCE(previous->'image_urls',previous->'media_urls','[]'::jsonb)))) THEN RAISE EXCEPTION 'Your plan allows at most % image(s) per post. Upgrade for more images.',max_images USING ERRCODE='23514';END IF;
  IF jsonb_typeof(videos)<>'array' OR (jsonb_array_length(videos)>max_videos AND (TG_OP='INSERT' OR NOT(videos <@ COALESCE(previous#>'{attributes,video_urls}',previous#>'{factory_details,video_urls}','[]'::jsonb)) OR jsonb_array_length(videos)>jsonb_array_length(COALESCE(previous#>'{attributes,video_urls}',previous#>'{factory_details,video_urls}','[]'::jsonb)))) THEN RAISE EXCEPTION 'Your plan allows at most % video(s) per post. Upgrade to Gold or Diamond for videos.',max_videos USING ERRCODE='23514';END IF;
  FOR item IN SELECT value FROM jsonb_array_elements(videos) WHERE NOT(COALESCE(previous#>'{attributes,video_urls}','[]'::jsonb) @> jsonb_build_array(value) OR COALESCE(previous#>'{factory_details,video_urls}','[]'::jsonb) @> jsonb_build_array(value)) OR TG_OP='INSERT' LOOP
   IF NOT EXISTS(SELECT 1 FROM provider_post_video_assets a WHERE a.provider_id=provider AND a.url=item#>>'{}' AND a.duration_seconds<=60) THEN RAISE EXCEPTION 'Your plan requires verified videos of no more than 60 seconds each. Upload the video again.' USING ERRCODE='23514';END IF;
  END LOOP;
 END IF;
 IF TG_OP='UPDATE' AND previous->>'published_at' IS NULL AND COALESCE(row_data->>'status',row_data->>'moderation_status') IN('pending','approved') AND COALESCE(row_data->>'status',row_data->>'moderation_status') IS DISTINCT FROM COALESCE(previous->>'status',previous->>'moderation_status') THEN
  SELECT (SELECT count(*) FROM products WHERE provider_id=provider AND moderation_status<>'archived' AND id<>NEW.id)+(SELECT count(*) FROM provider_listings WHERE provider_id=provider AND status<>'archived' AND id<>NEW.id)+1 INTO used;
  IF used>cap THEN RAISE EXCEPTION 'Your plan listing limit is exceeded. Archive listings or upgrade before publishing another listing.' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NEW;
END;$$;
CREATE OR REPLACE FUNCTION assign_provider_post_slot() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE provider uuid; cap integer; assigned integer; row_data jsonb; previous jsonb;
BEGIN
 provider:=NEW.provider_id;IF provider IS NULL THEN RETURN NEW;END IF;
 row_data:=to_jsonb(NEW);previous:=CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
 IF TG_OP='UPDATE' AND NEW.plan_slot IS DISTINCT FROM OLD.plan_slot THEN RAISE EXCEPTION 'Your plan slot cannot be changed directly.' USING ERRCODE='23514';END IF;
 IF TG_OP='INSERT' OR (COALESCE(previous->>'status',previous->>'moderation_status')='archived' AND COALESCE(row_data->>'status',row_data->>'moderation_status')<>'archived') THEN
  PERFORM 1 FROM provider_organizations WHERE id=provider FOR UPDATE;
  SELECT (provider_current_benefits(provider)->>'post_limit')::int INTO cap;
  SELECT n INTO assigned FROM generate_series(1,cap) n WHERE NOT EXISTS(SELECT 1 FROM products p WHERE p.provider_id=provider AND p.moderation_status<>'archived' AND p.id<>NEW.id AND p.plan_slot=n) AND NOT EXISTS(SELECT 1 FROM provider_listings l WHERE l.provider_id=provider AND l.status<>'archived' AND l.id<>NEW.id AND l.plan_slot=n) ORDER BY n LIMIT 1;
  IF assigned IS NULL THEN RAISE EXCEPTION 'Your plan post allowance is full. Archive a post or upgrade.' USING ERRCODE='23514';END IF;
  NEW.plan_slot:=assigned;
 END IF;
 RETURN NEW;
END;$$;

-- One business quota across staff accounts. Room capacity (10 providers) is unchanged.
CREATE FUNCTION enforce_provider_hall_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cap int;used int;
BEGIN
 IF NEW.role<>'provider' OR NEW.state<>'active' OR NEW.provider_id IS NULL OR NOT EXISTS(SELECT 1 FROM chat_rooms WHERE id=NEW.room_id AND kind='town_hall') THEN RETURN NEW;END IF;
 IF TG_OP='UPDATE' AND OLD.provider_id=NEW.provider_id AND OLD.room_id=NEW.room_id AND OLD.role='provider' AND OLD.state='active' THEN RETURN NEW;END IF;
 PERFORM 1 FROM provider_organizations WHERE id=NEW.provider_id FOR UPDATE;
 IF EXISTS(SELECT 1 FROM chat_participants p WHERE p.room_id=NEW.room_id AND p.provider_id=NEW.provider_id AND p.role='provider' AND p.state='active') THEN RETURN NEW;END IF;
 SELECT (provider_tier_benefits(badge_tier)->>'town_hall_room_limit')::int INTO cap FROM provider_public_identity WHERE provider_id=NEW.provider_id AND verified AND paid_active;
 IF COALESCE(cap,0)=0 THEN RAISE EXCEPTION 'Your plan needs verified paid access to join Town Hall.' USING ERRCODE='23514';END IF;
 SELECT count(DISTINCT p.room_id) INTO used FROM chat_participants p JOIN chat_rooms r ON r.id=p.room_id JOIN town_hall_requests h ON h.room_id=r.id
 WHERE p.provider_id=NEW.provider_id AND p.role='provider' AND p.state='active' AND r.kind='town_hall' AND r.state='open' AND h.engagement_deadline>clock_timestamp();
 IF used>=cap THEN RAISE EXCEPTION 'Your plan Town Hall room limit has been reached. Leave a request or upgrade before joining another.' USING ERRCODE='23514';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER provider_hall_capacity BEFORE INSERT OR UPDATE OF state,role,provider_id,room_id ON chat_participants FOR EACH ROW EXECUTE FUNCTION enforce_provider_hall_capacity();
CREATE INDEX provider_active_hall_quota ON chat_participants(provider_id,room_id) WHERE role='provider' AND state='active';
