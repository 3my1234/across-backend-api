-- Additive receipt-only scheduling. Existing send status/attempts are unchanged.
ALTER TABLE notification_push_deliveries
  ADD COLUMN receipt_claim UUID,
  ADD COLUMN receipt_lease_until TIMESTAMPTZ,
  ADD COLUMN receipt_next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
  ADD COLUMN receipt_attempts INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_push_receipt_claim_due
  ON notification_push_deliveries(receipt_next_attempt_at, sent_at, notification_id, push_token_id)
  WHERE status = 'sent' AND receipt_checked_at IS NULL AND expo_ticket_id IS NOT NULL;

-- One global receipt HTTP request in flight, at most one new request/second.
-- Crash recovery does not depend on an in-memory lock or a particular replica.
CREATE TABLE push_receipt_dispatch (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  owner UUID,
  lease_until TIMESTAMPTZ,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
  failures INTEGER NOT NULL DEFAULT 0
);
INSERT INTO push_receipt_dispatch(singleton) VALUES(TRUE);
