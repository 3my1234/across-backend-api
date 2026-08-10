CREATE TABLE IF NOT EXISTS admin_activity_state (
  admin_id UUID PRIMARY KEY REFERENCES admins(id) ON DELETE CASCADE,
  read_through_created_at TIMESTAMPTZ NOT NULL,
  read_through_event_id UUID NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS admin_activity_reads (
  admin_id UUID NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
  event_id UUID NOT NULL REFERENCES batch_events(id) ON DELETE CASCADE,
  read_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (admin_id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_admin_activity_reads_event
  ON admin_activity_reads(event_id, admin_id);

CREATE INDEX IF NOT EXISTS idx_batch_events_status_created_id
  ON batch_events(status, created_at DESC, id DESC);
