ALTER TYPE batch_status ADD VALUE IF NOT EXISTS 'closed';
ALTER TYPE batch_status ADD VALUE IF NOT EXISTS 'funds_sent_to_procurement';
ALTER TYPE batch_status ADD VALUE IF NOT EXISTS 'procurement_acknowledged';
ALTER TYPE batch_status ADD VALUE IF NOT EXISTS 'procurement_complete';
ALTER TYPE batch_status ADD VALUE IF NOT EXISTS 'ready_for_pickup';
