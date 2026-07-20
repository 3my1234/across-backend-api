CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TYPE order_status AS ENUM (
  'Pending',
  'Paid',
  'Shipped',
  'Delivered',
  'Completed',
  'Disputed',
  'Cancelled'
);

CREATE TYPE escrow_status AS ENUM ('held_in_escrow', 'released', 'frozen');
CREATE TYPE dispute_status AS ENUM ('none', 'active', 'resolved');
CREATE TYPE tracking_stage AS ENUM (
  'Order Placed',
  'Arrived at China Hub',
  'In Transit Internationally',
  'Arrived at Local Hub',
  'Out for Delivery',
  'Delivered'
);

CREATE TABLE countries_config (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  country_code CHAR(2) NOT NULL UNIQUE,
  currency_code CHAR(3) NOT NULL,
  base_escrow_days INTEGER NOT NULL DEFAULT 14 CHECK (base_escrow_days > 0),
  active_payment_gateways TEXT[] NOT NULL DEFAULT ARRAY['flutterwave'],
  vat_rate_bps INTEGER NOT NULL DEFAULT 0 CHECK (vat_rate_bps >= 0),
  is_active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  country_id UUID NOT NULL REFERENCES countries_config(id),
  email CITEXT UNIQUE,
  phone TEXT UNIQUE,
  password_hash TEXT NOT NULL,
  full_name TEXT NOT NULL,
  avatar_url TEXT NOT NULL DEFAULT '',
  region TEXT NOT NULL DEFAULT '',
  date_of_birth DATE,
  address TEXT NOT NULL DEFAULT '',
  city TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT '',
  postal_code TEXT NOT NULL DEFAULT '',
  default_shipping_address JSONB NOT NULL DEFAULT '{}'::jsonb,
  default_billing_address JSONB NOT NULL DEFAULT '{}'::jsonb,
  flutterwave_token TEXT,
  email_verified BOOLEAN NOT NULL DEFAULT false,
  verification_token TEXT,
  verification_token_expires_at TIMESTAMPTZ,
  verification_sent_at TIMESTAMPTZ,
  verification_resend_count INTEGER NOT NULL DEFAULT 0,
  is_active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (email IS NOT NULL OR phone IS NOT NULL)
);

CREATE TABLE logistics_hubs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  country_id UUID REFERENCES countries_config(id),
  code TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  hub_type TEXT NOT NULL CHECK (hub_type IN ('supplier_warehouse', 'sorting', 'clearance', 'last_mile')),
  city TEXT NOT NULL,
  address TEXT NOT NULL,
  timezone TEXT NOT NULL DEFAULT 'UTC',
  is_active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TYPE batch_status AS ENUM (
  'collecting_funds',
  'settled',
  'funds_sent_to_china',
  'purchasing',
  'enroute_nigeria',
  'arrived_local',
  'sorted',
  'completed'
);

CREATE TABLE order_batches (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_code TEXT NOT NULL UNIQUE,
  country_id UUID NOT NULL REFERENCES countries_config(id),
  batch_date DATE NOT NULL,
  status batch_status NOT NULL DEFAULT 'collecting_funds',
  transport_mode TEXT NOT NULL DEFAULT 'air' CHECK (transport_mode IN ('air', 'sea')),
  total_ngn_collected NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (total_ngn_collected >= 0),
  total_cny_sent NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (total_cny_sent >= 0),
  current_location TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(country_id, batch_date)
);

-- Per-category SKU counters for sequential SKUs like MCL-000001
CREATE TABLE sku_counters (
  category_code TEXT PRIMARY KEY,
  next_value INTEGER NOT NULL CHECK (next_value > 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE products (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_id UUID,
  origin_hub_id UUID REFERENCES logistics_hubs(id),
  sku TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  category_path TEXT[] NOT NULL DEFAULT '{}',
  variants JSONB NOT NULL DEFAULT '[]'::jsonb,
  image_urls TEXT[] NOT NULL DEFAULT '{}',
  cost_price_rmb NUMERIC(14,2) NOT NULL CHECK (cost_price_rmb >= 0),
  local_currency_code CHAR(3) NOT NULL DEFAULT 'NGN',
  local_selling_price NUMERIC(14,2) NOT NULL CHECK (local_selling_price >= 0),
  compare_at_price NUMERIC(14,2),
  exchange_rate_snapshot NUMERIC(18,6) NOT NULL CHECK (exchange_rate_snapshot > 0),
  inventory_count INTEGER NOT NULL DEFAULT 0 CHECK (inventory_count >= 0),
  factory_details JSONB NOT NULL DEFAULT '{}'::jsonb,
  is_active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE orders (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id),
  country_id UUID NOT NULL REFERENCES countries_config(id),
  batch_id UUID REFERENCES order_batches(id),
  currency_code CHAR(3) NOT NULL,
  total_amount NUMERIC(14,2) NOT NULL CHECK (total_amount >= 0),
  shipping_fee NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (shipping_fee >= 0),
  customs_fee NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (customs_fee >= 0),
  vat_fee NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (vat_fee >= 0),
  order_status order_status NOT NULL DEFAULT 'Pending',
  current_tracking_stage tracking_stage NOT NULL DEFAULT 'Order Placed',
  ready_for_manual_settlement BOOLEAN NOT NULL DEFAULT false,
  package_label TEXT,
  delivery_promised_at TIMESTAMPTZ,
  delivered_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE order_items (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  product_id UUID NOT NULL REFERENCES products(id),
  origin_hub_id UUID REFERENCES logistics_hubs(id),
  sku TEXT NOT NULL,
  title TEXT NOT NULL,
  variant JSONB NOT NULL DEFAULT '{}'::jsonb,
  quantity INTEGER NOT NULL CHECK (quantity > 0),
  unit_price NUMERIC(14,2) NOT NULL CHECK (unit_price >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE escrow_ledger (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id UUID NOT NULL UNIQUE REFERENCES orders(id) ON DELETE CASCADE,
  amount NUMERIC(14,2) NOT NULL CHECK (amount >= 0),
  currency_code CHAR(3) NOT NULL,
  escrow_status escrow_status NOT NULL DEFAULT 'held_in_escrow',
  escrow_lock_expiry TIMESTAMPTZ NOT NULL,
  dispute_status dispute_status NOT NULL DEFAULT 'none',
  flutterwave_tx_ref TEXT NOT NULL UNIQUE,
  flutterwave_transaction_id TEXT,
  released_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE reviews (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  order_id UUID REFERENCES orders(id) ON DELETE SET NULL,
  rating INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
  review_text TEXT NOT NULL DEFAULT '',
  media_urls TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(product_id, user_id, order_id)
);

CREATE TABLE tracking_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  hub_id UUID REFERENCES logistics_hubs(id),
  stage tracking_stage NOT NULL,
  barcode TEXT,
  notes TEXT NOT NULL DEFAULT '',
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE batch_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id UUID NOT NULL REFERENCES order_batches(id) ON DELETE CASCADE,
  actor_id UUID,
  event_type TEXT NOT NULL,
  status batch_status,
  location TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE admin_audit_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  actor_id UUID,
  action TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_id UUID,
  priority TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('normal', 'high', 'critical')),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE admins (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email CITEXT NOT NULL UNIQUE,
  full_name TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('super_admin', 'admin', 'catalog_admin', 'procurement_admin', 'courier_admin')),
  is_active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_products_category_path ON products USING gin(category_path);
CREATE INDEX idx_products_active_price ON products(is_active, local_selling_price);
CREATE INDEX idx_orders_user_created ON orders(user_id, created_at DESC);
CREATE INDEX idx_orders_status ON orders(order_status);
CREATE INDEX idx_orders_created_desc ON orders(created_at DESC);
CREATE INDEX idx_admins_role_active ON admins(role, is_active);
CREATE INDEX idx_batches_date_status ON order_batches(batch_date, status);
CREATE INDEX idx_escrow_worker_due ON escrow_ledger(escrow_lock_expiry)
  WHERE dispute_status = 'none' AND escrow_status = 'held_in_escrow';
CREATE INDEX idx_tracking_order_time ON tracking_events(order_id, occurred_at);
CREATE INDEX idx_batch_events_batch_time ON batch_events(batch_id, created_at);

-- Notifications
CREATE TABLE notifications (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  order_id UUID REFERENCES orders(id) ON DELETE SET NULL,
  batch_id UUID REFERENCES order_batches(id) ON DELETE SET NULL,
  type TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  data JSONB NOT NULL DEFAULT '{}'::jsonb,
  is_read BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_user_created ON notifications(user_id, created_at DESC);

-- XP System
CREATE TABLE xp_transactions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  amount INTEGER NOT NULL,
  reason TEXT NOT NULL,
  reference_id TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_xp_transactions_user ON xp_transactions(user_id, created_at DESC);
CREATE INDEX idx_xp_transactions_ref ON xp_transactions(reference_id);

CREATE TABLE xp_daily_login (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  claim_date DATE NOT NULL DEFAULT CURRENT_DATE,
  xp_awarded INTEGER NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(user_id, claim_date)
);

-- Materialized XP balance view
CREATE VIEW user_xp_balance AS
  SELECT user_id, COALESCE(SUM(amount), 0)::int AS total_xp
  FROM xp_transactions GROUP BY user_id;

-- Support tickets
CREATE TABLE support_tickets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  subject TEXT NOT NULL,
  message TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'in_progress', 'closed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE support_messages (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  ticket_id UUID NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
  sender_type TEXT NOT NULL CHECK (sender_type IN ('user', 'admin')),
  sender_id UUID NOT NULL,
  message TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
