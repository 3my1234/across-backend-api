-- Add stamp duty fee to orders (₦170 ≈ $0.11)
ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS stamp_duty_fee NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (stamp_duty_fee >= 0);