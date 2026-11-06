-- Ordinary shared-chat notifications identify a user, not a business. Avoid
-- planning/executing provider joins for that case. Keep provider identity,
-- malformed-ID handling and the existing transactional triggers unchanged.
CREATE OR REPLACE FUNCTION notification_sender_identity(data jsonb)
RETURNS jsonb LANGUAGE plpgsql STABLE AS $$
DECLARE sender uuid; provider uuid; identity jsonb;
BEGIN
 sender := notification_identity_uuid(data->>'sender_user_id');
 IF sender IS NULL THEN RETURN '{}'::jsonb; END IF;
 IF data->>'sender_is_provider'='true' THEN
  provider := notification_identity_uuid(data->>'provider_id');
  SELECT jsonb_build_object('sender_user_id',u.id,'sender_name',
   COALESCE(NULLIF(p.business_name,''),u.full_name),
   'sender_avatar_url',COALESCE(NULLIF(p.logo_url,''),u.avatar_url,''))
  INTO identity FROM users u LEFT JOIN provider_organizations p ON p.id=provider
   AND EXISTS(SELECT 1 FROM provider_members pm WHERE pm.provider_id=p.id AND pm.user_id=u.id AND pm.is_active)
  WHERE u.id=sender;
 ELSE
  SELECT jsonb_build_object('sender_user_id',u.id,'sender_name',u.full_name,
   'sender_avatar_url',COALESCE(u.avatar_url,''))
  INTO identity FROM users u WHERE u.id=sender;
 END IF;
 RETURN COALESCE(identity,'{}'::jsonb);
END $$;
