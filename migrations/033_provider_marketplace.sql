CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS provider_organizations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  business_name TEXT NOT NULL,
  slug TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  contact_email TEXT NOT NULL,
  contact_phone TEXT NOT NULL,
  website_url TEXT NOT NULL DEFAULT '',
  logo_url TEXT NOT NULL DEFAULT '',
  address_line TEXT NOT NULL DEFAULT '',
  city TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT '',
  country_code TEXT NOT NULL DEFAULT 'NG',
  verification_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (verification_status IN ('pending','approved','rejected','suspended')),
  verification_notes TEXT NOT NULL DEFAULT '',
  verified_at TIMESTAMPTZ,
  verified_by UUID REFERENCES admins(id) ON DELETE SET NULL,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_provider_owner ON provider_organizations(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_provider_verification_cursor ON provider_organizations(verification_status, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS provider_members (
  provider_id UUID NOT NULL REFERENCES provider_organizations(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'staff' CHECK (role IN ('owner','manager','staff')),
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (provider_id, user_id)
);

CREATE TABLE IF NOT EXISTS provider_verification_documents (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id UUID NOT NULL REFERENCES provider_organizations(id) ON DELETE CASCADE,
  document_type TEXT NOT NULL,
  document_url TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
  review_notes TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  reviewed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_provider_documents_provider ON provider_verification_documents(provider_id, created_at DESC);

CREATE TABLE IF NOT EXISTS provider_subscription_plans (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  amount_ngn NUMERIC(14,2) NOT NULL CHECK (amount_ngn > 0),
  billing_interval TEXT NOT NULL DEFAULT 'monthly' CHECK (billing_interval = 'monthly'),
  listing_limit INTEGER NOT NULL DEFAULT 20 CHECK (listing_limit > 0),
  flutterwave_plan_id BIGINT,
  features JSONB NOT NULL DEFAULT '{}'::jsonb,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS provider_subscriptions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id UUID NOT NULL REFERENCES provider_organizations(id) ON DELETE CASCADE,
  plan_id UUID NOT NULL REFERENCES provider_subscription_plans(id) ON DELETE RESTRICT,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending','active','past_due','grace','expired','cancelled')),
  tx_ref TEXT NOT NULL UNIQUE,
  flutterwave_transaction_id TEXT,
  flutterwave_subscription_id TEXT,
  customer_email TEXT NOT NULL,
  starts_at TIMESTAMPTZ,
  current_period_end TIMESTAMPTZ,
  grace_ends_at TIMESTAMPTZ,
  cancelled_at TIMESTAMPTZ,
  last_payment_at TIMESTAMPTZ,
  version BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_provider_subscription_access ON provider_subscriptions(provider_id, status, current_period_end DESC);

CREATE TABLE IF NOT EXISTS provider_subscription_payments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  subscription_id UUID NOT NULL REFERENCES provider_subscriptions(id) ON DELETE CASCADE,
  flutterwave_transaction_id TEXT NOT NULL UNIQUE,
  tx_ref TEXT NOT NULL,
  amount NUMERIC(14,2) NOT NULL,
  currency_code TEXT NOT NULL,
  paid_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_provider_subscription_payments_history ON provider_subscription_payments(subscription_id, paid_at DESC);

CREATE TABLE IF NOT EXISTS provider_listings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id UUID NOT NULL REFERENCES provider_organizations(id) ON DELETE CASCADE,
  listing_type TEXT NOT NULL CHECK (listing_type IN ('hotel','short_let','car_rental','car_wash','shop_rental','property','land')),
  title TEXT NOT NULL,
  slug TEXT NOT NULL,
  description TEXT NOT NULL,
  category TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft','pending','approved','rejected','suspended','archived')),
  contact_email TEXT NOT NULL DEFAULT '',
  contact_phone TEXT NOT NULL DEFAULT '',
  address_line TEXT NOT NULL DEFAULT '',
  city TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT '',
  country_code TEXT NOT NULL DEFAULT 'NG',
  latitude NUMERIC(9,6),
  longitude NUMERIC(9,6),
  price NUMERIC(14,2),
  currency_code TEXT NOT NULL DEFAULT 'NGN',
  pricing_unit TEXT NOT NULL DEFAULT '',
  capacity INTEGER NOT NULL DEFAULT 1 CHECK (capacity > 0),
  media_urls TEXT[] NOT NULL DEFAULT '{}',
  attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
  moderation_notes TEXT NOT NULL DEFAULT '',
  moderated_by UUID REFERENCES admins(id) ON DELETE SET NULL,
  moderated_at TIMESTAMPTZ,
  published_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider_id, slug)
);
CREATE INDEX IF NOT EXISTS idx_provider_listings_public_cursor ON provider_listings(listing_type, status, published_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_provider_listings_provider_cursor ON provider_listings(provider_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_provider_listings_search ON provider_listings USING GIN ((title || ' ' || description || ' ' || city || ' ' || state) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_provider_listings_attributes ON provider_listings USING GIN (attributes);

CREATE TABLE IF NOT EXISTS provider_availability_slots (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  listing_id UUID NOT NULL REFERENCES provider_listings(id) ON DELETE CASCADE,
  starts_at TIMESTAMPTZ NOT NULL,
  ends_at TIMESTAMPTZ NOT NULL,
  capacity INTEGER NOT NULL DEFAULT 1 CHECK (capacity > 0),
  remaining INTEGER NOT NULL DEFAULT 1 CHECK (remaining >= 0),
  status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','blocked','closed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (ends_at > starts_at),
  UNIQUE (listing_id, starts_at, ends_at)
);
CREATE INDEX IF NOT EXISTS idx_provider_slots_lookup ON provider_availability_slots(listing_id, status, starts_at, ends_at);

CREATE TABLE IF NOT EXISTS provider_requests (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  listing_id UUID NOT NULL REFERENCES provider_listings(id) ON DELETE RESTRICT,
  provider_id UUID NOT NULL REFERENCES provider_organizations(id) ON DELETE RESTRICT,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  request_type TEXT NOT NULL CHECK (request_type IN ('booking','appointment','inspection','enquiry')),
  slot_id UUID REFERENCES provider_availability_slots(id) ON DELETE SET NULL,
  starts_at TIMESTAMPTZ,
  ends_at TIMESTAMPTZ,
  party_size INTEGER NOT NULL DEFAULT 1 CHECK (party_size > 0),
  message TEXT NOT NULL DEFAULT '',
  search_text TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending','accepted','rejected','cancelled','completed')),
  idempotency_key TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, idempotency_key)
);
ALTER TABLE provider_requests
  ADD COLUMN IF NOT EXISTS search_text TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_provider_requests_provider_cursor ON provider_requests(provider_id, status, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_provider_requests_user_cursor ON provider_requests(user_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_provider_requests_search_trgm ON provider_requests USING gin(search_text gin_trgm_ops);

CREATE TABLE IF NOT EXISTS provider_contact_reveals (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  listing_id UUID NOT NULL REFERENCES provider_listings(id) ON DELETE CASCADE,
  provider_id UUID NOT NULL REFERENCES provider_organizations(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  safety_acknowledged BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_provider_contact_reveals_audit ON provider_contact_reveals(provider_id, created_at DESC);

CREATE TABLE IF NOT EXISTS provider_listing_reports (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  listing_id UUID NOT NULL REFERENCES provider_listings(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  reason TEXT NOT NULL,
  details TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','reviewing','resolved','dismissed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_provider_reports_admin_cursor ON provider_listing_reports(status, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS provider_marketplace_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id UUID REFERENCES provider_organizations(id) ON DELETE SET NULL,
  listing_id UUID REFERENCES provider_listings(id) ON DELETE SET NULL,
  actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  actor_admin_id UUID REFERENCES admins(id) ON DELETE SET NULL,
  event_type TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_provider_events_cursor ON provider_marketplace_events(created_at DESC, id DESC);
