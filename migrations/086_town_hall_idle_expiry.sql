ALTER TABLE town_hall_requests ADD COLUMN engagement_deadline timestamptz;
ALTER TABLE town_hall_requests ADD COLUMN first_provider_engaged_at timestamptz;
ALTER TABLE town_hall_requests ADD COLUMN expired_at timestamptz;
UPDATE town_hall_requests h SET engagement_deadline=r.created_at+interval '5 minutes'
FROM chat_rooms r WHERE r.id=h.room_id;
ALTER TABLE town_hall_requests ALTER COLUMN engagement_deadline SET NOT NULL;
ALTER TABLE town_hall_requests ALTER COLUMN engagement_deadline SET DEFAULT (clock_timestamp()+interval '5 minutes');

-- Existing provider participation must never be mistaken for an unanswered request.
UPDATE town_hall_requests h SET first_provider_engaged_at=(
 SELECT min(at) FROM (
  SELECT a.created_at AS at FROM town_hall_applications a WHERE a.room_id=h.room_id
  UNION ALL SELECT p.joined_at FROM chat_participants p WHERE p.room_id=h.room_id AND p.role='provider'
  UNION ALL SELECT o.created_at FROM town_hall_offers o WHERE o.room_id=h.room_id
  UNION ALL SELECT m.created_at FROM chat_messages m JOIN chat_rooms r ON r.id=m.room_id
    WHERE m.room_id=h.room_id AND m.sender_user_id<>r.owner_user_id
 ) evidence
);
CREATE INDEX town_hall_unanswered_deadline ON town_hall_requests(engagement_deadline,room_id)
 WHERE first_provider_engaged_at IS NULL AND expired_at IS NULL;

CREATE FUNCTION record_town_hall_provider_engagement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE room_kind text; room_state text; deadline timestamptz; engaged timestamptz;
BEGIN
 IF TG_TABLE_NAME='chat_participants' THEN
  IF NEW.role<>'provider' OR NEW.state<>'active' THEN RETURN NEW; END IF;
 ELSE
  IF NEW.state NOT IN('pending','admitted') THEN RETURN NEW; END IF;
 END IF;
 -- Admission, application and expiry serialize on the same room row.
 SELECT kind,state INTO room_kind,room_state FROM chat_rooms WHERE id=NEW.room_id FOR UPDATE;
 IF room_kind IS DISTINCT FROM 'town_hall' THEN RETURN NEW; END IF;
 SELECT engagement_deadline,first_provider_engaged_at INTO deadline,engaged
 FROM town_hall_requests WHERE room_id=NEW.room_id;
 IF NOT FOUND THEN RETURN NEW; END IF;
 IF room_state<>'open' OR (engaged IS NULL AND deadline<=clock_timestamp()) THEN
  RAISE EXCEPTION 'This request has closed or expired. Ask the buyer to repost it.' USING ERRCODE='P0409';
 END IF;
 UPDATE town_hall_requests SET first_provider_engaged_at=COALESCE(first_provider_engaged_at,clock_timestamp())
 WHERE room_id=NEW.room_id AND first_provider_engaged_at IS NULL;
 RETURN NEW;
END; $$;
CREATE TRIGGER town_hall_application_engagement BEFORE INSERT OR UPDATE OF state ON town_hall_applications
 FOR EACH ROW EXECUTE FUNCTION record_town_hall_provider_engagement();
CREATE TRIGGER town_hall_participant_engagement BEFORE INSERT OR UPDATE OF state ON chat_participants
 FOR EACH ROW EXECUTE FUNCTION record_town_hall_provider_engagement();

CREATE FUNCTION expire_unengaged_town_hall_requests(batch_limit integer DEFAULT 250) RETURNS integer LANGUAGE plpgsql AS $$
DECLARE affected integer;
BEGIN
 WITH candidates AS MATERIALIZED (
  SELECT r.id FROM town_hall_requests h JOIN chat_rooms r ON r.id=h.room_id
  WHERE r.state='open' AND h.first_provider_engaged_at IS NULL AND h.expired_at IS NULL
    AND h.engagement_deadline<=clock_timestamp()
  ORDER BY h.engagement_deadline,r.id LIMIT GREATEST(1,LEAST(batch_limit,1000))
  FOR UPDATE OF r SKIP LOCKED
 ), marked AS (
  UPDATE town_hall_requests h SET expired_at=clock_timestamp() FROM candidates c
  WHERE h.room_id=c.id AND h.first_provider_engaged_at IS NULL AND h.expired_at IS NULL
    AND h.engagement_deadline<=clock_timestamp() RETURNING h.room_id
 ) UPDATE chat_rooms r SET state='closed',closed_at=clock_timestamp(),revision=revision+1
 FROM marked m WHERE r.id=m.room_id;
 GET DIAGNOSTICS affected=ROW_COUNT;
 RETURN affected;
END; $$;
