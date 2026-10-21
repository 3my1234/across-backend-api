-- Replace the unanswered-only five-minute rule with a hard 24-hour lifetime.
-- Keep the existing column/function names so a rolling worker upgrade is safe.
UPDATE town_hall_requests h SET engagement_deadline=r.created_at+interval '24 hours'
FROM chat_rooms r WHERE r.id=h.room_id;
ALTER TABLE town_hall_requests ALTER COLUMN engagement_deadline SET DEFAULT (clock_timestamp()+interval '24 hours');
CREATE INDEX town_hall_lifetime_deadline ON town_hall_requests(engagement_deadline,room_id);

CREATE OR REPLACE FUNCTION record_town_hall_provider_engagement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE room_kind text; room_state text; deadline timestamptz;
BEGIN
 IF TG_TABLE_NAME='chat_participants' THEN
  IF NEW.role<>'provider' OR NEW.state<>'active' THEN RETURN NEW; END IF;
 ELSE
  IF NEW.state NOT IN('pending','admitted') THEN RETURN NEW; END IF;
 END IF;
 SELECT kind,state INTO room_kind,room_state FROM chat_rooms WHERE id=NEW.room_id FOR UPDATE;
 IF room_kind IS DISTINCT FROM 'town_hall' THEN RETURN NEW; END IF;
 SELECT engagement_deadline INTO deadline FROM town_hall_requests WHERE room_id=NEW.room_id;
 IF room_state<>'open' OR deadline<=clock_timestamp() THEN
  RAISE EXCEPTION 'This request has ended or expired.' USING ERRCODE='P0409';
 END IF;
 UPDATE town_hall_requests SET first_provider_engaged_at=COALESCE(first_provider_engaged_at,clock_timestamp()) WHERE room_id=NEW.room_id;
 RETURN NEW;
END; $$;

CREATE OR REPLACE FUNCTION expire_unengaged_town_hall_requests(batch_limit integer DEFAULT 250) RETURNS integer LANGUAGE plpgsql AS $$
DECLARE affected integer;
BEGIN
 WITH candidates AS MATERIALIZED (
  SELECT r.id FROM town_hall_requests h JOIN chat_rooms r ON r.id=h.room_id
  WHERE r.state='open' AND h.engagement_deadline<=clock_timestamp()
  ORDER BY h.engagement_deadline,r.id LIMIT GREATEST(1,LEAST(batch_limit,1000))
  FOR UPDATE OF r SKIP LOCKED
 ), marked AS (
  UPDATE town_hall_requests h SET expired_at=clock_timestamp() FROM candidates c WHERE h.room_id=c.id RETURNING h.room_id
 ) UPDATE chat_rooms r SET state='closed',closed_at=clock_timestamp(),revision=revision+1 FROM marked m WHERE r.id=m.room_id;
 GET DIAGNOSTICS affected=ROW_COUNT;
 RETURN affected;
END; $$;

CREATE FUNCTION purge_ended_town_hall_requests(batch_limit integer DEFAULT 250) RETURNS integer LANGUAGE plpgsql AS $$
DECLARE affected integer;
BEGIN
 PERFORM expire_unengaged_town_hall_requests(batch_limit);
 -- Reports keep their evidence for admin review, hidden from ordinary inboxes.
 -- Direct chats contain an independent offer snapshot and are never deleted.
 WITH candidates AS MATERIALIZED (
  SELECT r.id FROM chat_rooms r WHERE r.kind='town_hall' AND r.state='closed'
    AND NOT EXISTS(SELECT 1 FROM chat_reports q WHERE q.room_id=r.id)
  ORDER BY r.closed_at,r.id LIMIT GREATEST(1,LEAST(batch_limit,1000))
  FOR UPDATE OF r SKIP LOCKED
 ) DELETE FROM chat_rooms r USING candidates c WHERE r.id=c.id;
 GET DIAGNOSTICS affected=ROW_COUNT;
 RETURN affected;
END; $$;
