-- Complaints/Refunds Table
CREATE TABLE IF NOT EXISTS complaints (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id UUID REFERENCES orders(id) ON DELETE SET NULL,
  product_id UUID REFERENCES products(id) ON DELETE SET NULL,
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  admin_id UUID REFERENCES admins(id) ON DELETE SET NULL,
  description TEXT NOT NULL,
  refund_amount NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (refund_amount >= 0),
  status TEXT NOT NULL DEFAULT 'unresolved' CHECK (status IN ('unresolved', 'resolved')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_complaints_status ON complaints(status);
CREATE INDEX IF NOT EXISTS idx_complaints_created ON complaints(created_at);

-- View for daily sales analytics
CREATE OR REPLACE VIEW daily_sales AS
  SELECT
    DATE(created_at) AS sale_date,
    COUNT(*) AS order_count,
    COALESCE(SUM(total_amount), 0) AS total_revenue
  FROM orders
  WHERE order_status = 'Paid'
  GROUP BY DATE(created_at)
  ORDER BY sale_date DESC;

-- View for daily loss from resolved complaints
CREATE OR REPLACE VIEW daily_losses AS
  SELECT
    DATE(created_at) AS loss_date,
    COUNT(*) AS complaint_count,
    COALESCE(SUM(refund_amount), 0) AS total_refunded
  FROM complaints
  WHERE status = 'resolved'
  GROUP BY DATE(created_at)
  ORDER BY loss_date DESC;