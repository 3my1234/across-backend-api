-- Snapshot public sender identity alongside existing immutable routing data.
CREATE FUNCTION notification_identity_uuid(value text) RETURNS uuid LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN RETURN value::uuid; EXCEPTION WHEN invalid_text_representation THEN RETURN NULL; END $$;
CREATE FUNCTION notification_sender_identity(data jsonb) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT COALESCE((SELECT jsonb_build_object('sender_user_id',u.id,'sender_name',
 CASE WHEN data->>'sender_is_provider'='true' THEN COALESCE(NULLIF(p.business_name,''),u.full_name) ELSE u.full_name END,
 'sender_avatar_url',CASE WHEN data->>'sender_is_provider'='true' THEN COALESCE(NULLIF(p.logo_url,''),u.avatar_url,'') ELSE COALESCE(u.avatar_url,'') END)
 FROM users u LEFT JOIN provider_organizations p ON p.id=notification_identity_uuid(data->>'provider_id')
 AND EXISTS(SELECT 1 FROM provider_members pm WHERE pm.provider_id=p.id AND pm.user_id=u.id AND pm.is_active)
 WHERE u.id=notification_identity_uuid(data->>'sender_user_id')), '{}'::jsonb)
$$;
CREATE FUNCTION attach_notification_sender() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source jsonb;
BEGIN
 source:=NEW.data;
 IF source ? 'provider_event_id' THEN
  SELECT COALESCE(metadata,'{}'::jsonb) INTO source FROM provider_marketplace_events WHERE id=notification_identity_uuid(NEW.data->>'provider_event_id');
 END IF;
 NEW.data:=NEW.data||notification_sender_identity(COALESCE(source,'{}'::jsonb));
 RETURN NEW;
END $$;
CREATE TRIGGER notification_sender BEFORE INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION attach_notification_sender();
CREATE FUNCTION attach_web_push_sender() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source jsonb;
BEGIN
 IF NEW.source_key LIKE 'provider-event:%' THEN
  SELECT metadata INTO source FROM provider_marketplace_events WHERE id=notification_identity_uuid(split_part(NEW.source_key,':',2));
 ELSIF NEW.source_key LIKE 'notification:%' THEN
  SELECT data INTO source FROM notifications WHERE id=notification_identity_uuid(split_part(NEW.source_key,':',2));
 END IF;
 NEW.payload:=NEW.payload||notification_sender_identity(COALESCE(source,'{}'::jsonb));
 RETURN NEW;
END $$;
CREATE TRIGGER web_push_sender BEFORE INSERT ON web_push_deliveries FOR EACH ROW EXECUTE FUNCTION attach_web_push_sender();
