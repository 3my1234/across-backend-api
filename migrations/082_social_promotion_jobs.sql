CREATE TABLE social_promotion_jobs (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 requested_at timestamptz NOT NULL DEFAULT now(),
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0
);
CREATE INDEX social_promotion_job_age ON social_promotion_jobs(next_attempt_at,requested_at,user_id);
