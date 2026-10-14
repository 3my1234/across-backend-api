-- Preserve structured requests; new message-only requests need no invented price.
ALTER TABLE town_hall_requests ADD COLUMN message_only boolean NOT NULL DEFAULT false;
ALTER TABLE town_hall_requests ALTER COLUMN quantity DROP NOT NULL;
ALTER TABLE town_hall_requests ALTER COLUMN target_unit_price DROP NOT NULL;
