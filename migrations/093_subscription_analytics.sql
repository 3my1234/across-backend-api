-- Bound reporting to recent verified NGN subscription receipts.
CREATE INDEX IF NOT EXISTS payments_subscription_analytics_paid_at
 ON payments(paid_at) INCLUDE(amount,refund_status,chargeback_status)
 WHERE purpose='provider_subscription' AND payment_status='succeeded' AND currency_code='NGN';
