\set ON_ERROR_STOP on
-- Explicit execution changes approvals. Supply -v emails='a@example.com,b@example.com'.
-- Adds signup access without creating a waitlist entry or consent record.
BEGIN;
WITH requested AS (
  SELECT DISTINCT lower(btrim(value)) AS email
  FROM unnest(string_to_array(:'emails', ',')) AS value WHERE btrim(value) <> ''
), eligible AS (
  SELECT email FROM requested WHERE length(email)<=254 AND email ~ '^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$'
), changed AS (
  INSERT INTO signup_approved_emails(email) SELECT email FROM eligible
  ON CONFLICT(email) DO NOTHING RETURNING email
)
SELECT r.email, CASE WHEN e.email IS NULL THEN 'invalid email'
                    WHEN c.email IS NOT NULL THEN 'approved'
                    ELSE 'already approved' END AS result
FROM requested r LEFT JOIN changed c ON c.email=r.email
LEFT JOIN eligible e ON e.email=r.email ORDER BY r.email;
COMMIT;
