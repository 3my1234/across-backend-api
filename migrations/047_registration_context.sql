ALTER TABLE users
  ADD COLUMN IF NOT EXISTS registration_context TEXT NOT NULL DEFAULT 'buyer'
  CHECK (registration_context IN ('buyer', 'provider'));
