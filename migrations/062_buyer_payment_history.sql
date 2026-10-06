CREATE INDEX IF NOT EXISTS idx_payments_buyer_history
ON payments(user_id,created_at DESC,id DESC) WHERE purpose='order';
