CREATE TABLE launch_waitlist (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 email TEXT NOT NULL UNIQUE CHECK(length(email)<=254 AND email=lower(email)),
 full_name TEXT NOT NULL DEFAULT '',
 phone TEXT NOT NULL DEFAULT '',
 interest TEXT NOT NULL DEFAULT 'exploring' CHECK(interest IN ('exploring','buyer','seller','artisan')),
 source TEXT NOT NULL DEFAULT 'website',
 consent_version TEXT NOT NULL DEFAULT 'launch-updates-v1',
 consented_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX launch_waitlist_created ON launch_waitlist(created_at DESC,id DESC);
