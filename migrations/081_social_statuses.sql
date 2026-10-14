CREATE TABLE social_statuses (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 client_id uuid NOT NULL,
 body text NOT NULL CHECK(char_length(body) BETWEEN 1 AND 2000),
 media_keys text[] NOT NULL DEFAULT '{}',
 audience text NOT NULL CHECK(audience IN ('friends','selected')),
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '24 hours',
 deleted_at timestamptz,
 UNIQUE(user_id,client_id)
);
CREATE INDEX social_status_feed ON social_statuses(user_id,created_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE INDEX social_status_expiry ON social_statuses(expires_at) WHERE deleted_at IS NULL;
CREATE TABLE social_status_recipients (
 status_id uuid NOT NULL REFERENCES social_statuses(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 PRIMARY KEY(status_id,user_id)
);
