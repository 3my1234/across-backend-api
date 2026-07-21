ALTER TABLE users
  ADD COLUMN IF NOT EXISTS privy_user_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS users_privy_user_id_unique
  ON users(privy_user_id)
  WHERE privy_user_id IS NOT NULL;