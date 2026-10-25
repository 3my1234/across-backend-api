-- Preserve current accounts; only expand the role vocabulary and add access audit.
ALTER TABLE admins DROP CONSTRAINT IF EXISTS admins_role_check;
ALTER TABLE admins ADD CONSTRAINT admins_role_check CHECK(role IN
 ('super_admin','catalog_admin','support_admin','moderation_admin','finance_admin','ads_admin'));
CREATE TABLE admin_access_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 actor_admin_id uuid REFERENCES admins(id) ON DELETE SET NULL,
 target_admin_id uuid REFERENCES admins(id) ON DELETE SET NULL,
 action text NOT NULL CHECK(action IN ('created','access_updated','password_reset')),
 previous_access jsonb NOT NULL DEFAULT '{}'::jsonb,
 new_access jsonb NOT NULL DEFAULT '{}'::jsonb,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX admin_access_events_created ON admin_access_events(created_at DESC,id DESC);
