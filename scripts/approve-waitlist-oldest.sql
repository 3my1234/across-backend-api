\set ON_ERROR_STOP on
-- Explicit execution changes approvals. Supply -v count=N (1 through 1000).
BEGIN;
CREATE TEMP TABLE approval_limit AS SELECT :'count'::integer AS n;
DO $$ BEGIN
  IF (SELECT n FROM approval_limit) NOT BETWEEN 1 AND 1000 THEN
    RAISE EXCEPTION 'count must be between 1 and 1000';
  END IF;
END $$;
WITH picked AS (
  SELECT id FROM launch_waitlist WHERE NOT approved
  ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT (SELECT n FROM approval_limit)
)
UPDATE launch_waitlist w SET approved=true FROM picked p WHERE w.id=p.id
RETURNING w.email, 'approved' AS result;
DROP TABLE approval_limit;
COMMIT;
