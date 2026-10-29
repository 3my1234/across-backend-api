-- Isolated benchmark candidate only; NOT a production migration.
-- Provider identity still requires active membership; ordinary user snapshots
-- avoid provider joins and UUID parsing that cannot affect their result.
CREATE OR REPLACE FUNCTION notification_sender_identity(data jsonb) RETURNS jsonb LANGUAGE sql STABLE AS $$
SELECT CASE WHEN data->>'sender_is_provider'='true' THEN
 COALESCE((SELECT jsonb_build_object('sender_user_id',u.id,
 'sender_name',COALESCE(NULLIF(p.business_name,''),u.full_name),
 'sender_avatar_url',COALESCE(NULLIF(p.logo_url,''),u.avatar_url,''))
 FROM users u LEFT JOIN provider_organizations p ON p.id=notification_identity_uuid(data->>'provider_id')
 AND EXISTS(SELECT 1 FROM provider_members pm WHERE pm.provider_id=p.id AND pm.user_id=u.id AND pm.is_active)
 WHERE u.id=notification_identity_uuid(data->>'sender_user_id')), '{}'::jsonb)
 ELSE COALESCE((SELECT jsonb_build_object('sender_user_id',u.id,
 'sender_name',u.full_name,'sender_avatar_url',COALESCE(u.avatar_url,''))
 FROM users u WHERE u.id=notification_identity_uuid(data->>'sender_user_id')), '{}'::jsonb)
 END;
$$;
