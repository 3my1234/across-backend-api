ALTER TABLE users
  ADD COLUMN IF NOT EXISTS email_verified BOOLEAN,
  ADD COLUMN IF NOT EXISTS verification_token TEXT,
  ADD COLUMN IF NOT EXISTS verification_token_expires_at TIMESTAMPTZ;

UPDATE users
SET email_verified = true
WHERE email_verified IS NULL;

ALTER TABLE users
  ALTER COLUMN email_verified SET DEFAULT false,
  ALTER COLUMN email_verified SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_verification_token
  ON users(verification_token)
  WHERE verification_token IS NOT NULL;