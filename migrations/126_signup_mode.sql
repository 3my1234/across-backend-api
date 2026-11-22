-- Additive; default preserves current signups until changed by a super admin.
CREATE TABLE signup_policy (singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton), mode text NOT NULL DEFAULT 'open' CHECK (mode IN ('open','approved_only','closed')), updated_at timestamptz NOT NULL DEFAULT now());
INSERT INTO signup_policy(singleton,mode) VALUES(true,'open');
ALTER TABLE launch_waitlist ADD COLUMN approved boolean NOT NULL DEFAULT false;
-- A separate access list permits email corrections without inventing waitlist consent.
CREATE TABLE signup_approved_emails (
 email text PRIMARY KEY CHECK(email=lower(email) AND length(email)<=254),
 created_at timestamptz NOT NULL DEFAULT now()
);
