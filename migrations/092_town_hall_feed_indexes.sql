-- Match the bounded, creation-ordered public feed and buyer history cursors.
-- No data, eligibility or existing-listing changes.
CREATE INDEX IF NOT EXISTS town_hall_feed_created
 ON chat_rooms(created_at DESC,id DESC)
 WHERE kind='town_hall' AND state='open';
CREATE INDEX IF NOT EXISTS town_hall_owner_created
 ON chat_rooms(owner_user_id,created_at DESC,id DESC)
 WHERE kind='town_hall';
