CREATE TABLE IF NOT EXISTS admins (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email CITEXT NOT NULL UNIQUE,
  full_name TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('super_admin', 'admin')),
  is_active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE products
  ADD COLUMN IF NOT EXISTS compare_at_price NUMERIC(14,2),
  ADD COLUMN IF NOT EXISTS factory_details JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS customs_fee NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (customs_fee >= 0),
  ADD COLUMN IF NOT EXISTS vat_fee NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (vat_fee >= 0);

CREATE INDEX IF NOT EXISTS idx_admins_role_active ON admins(role, is_active);
CREATE INDEX IF NOT EXISTS idx_orders_created_desc ON orders(created_at DESC);
