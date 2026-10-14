CREATE TABLE social_promotion_jobs (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 requested_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX social_promotion_job_age ON social_promotion_jobs(requested_at,user_id);
