CREATE TABLE IF NOT EXISTS provider_conversations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  listing_id UUID NOT NULL REFERENCES provider_listings(id) ON DELETE RESTRICT,
  provider_id UUID NOT NULL REFERENCES provider_organizations(id) ON DELETE RESTRICT,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed','blocked')),
  buyer_last_read_at TIMESTAMPTZ,
  provider_last_read_at TIMESTAMPTZ,
  last_message_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (listing_id, user_id)
);

CREATE TABLE IF NOT EXISTS provider_conversation_messages (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  conversation_id UUID NOT NULL REFERENCES provider_conversations(id) ON DELETE CASCADE,
  sender_type TEXT NOT NULL CHECK (sender_type IN ('buyer','provider')),
  sender_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  body TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_provider_conversations_buyer
  ON provider_conversations(user_id, last_message_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_provider_conversations_provider
  ON provider_conversations(provider_id, last_message_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_provider_conversation_messages
  ON provider_conversation_messages(conversation_id, created_at, id);
