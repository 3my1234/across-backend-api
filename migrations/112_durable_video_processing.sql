-- Dedicated opt-in video processing. Existing inline completions remain compatible.
ALTER TABLE provider_video_uploads DROP CONSTRAINT provider_video_uploads_state_check;
ALTER TABLE provider_video_uploads ADD CONSTRAINT provider_video_uploads_state_check CHECK(state IN('pending','queued','processing','complete','failed'));
ALTER TABLE provider_video_uploads ADD COLUMN processing_mode text NOT NULL DEFAULT 'inline' CHECK(processing_mode IN('inline','background')),
 ADD COLUMN claim_token uuid, ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 3),
 ADD COLUMN available_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN error_code text,
 ADD COLUMN thumbnail_url text;
ALTER TABLE provider_video_launch_policy ADD COLUMN queue_limit integer NOT NULL DEFAULT 10 CHECK(queue_limit BETWEEN 1 AND 100),
 ADD COLUMN worker_token uuid, ADD COLUMN worker_lease_until timestamptz;
CREATE INDEX provider_video_processing_due ON provider_video_uploads(available_at,created_at) WHERE processing_mode='background' AND state IN('queued','processing');
