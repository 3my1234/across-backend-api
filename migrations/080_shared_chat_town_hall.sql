-- Additive foundation. Existing marketplace conversations are exposed through
-- the shared inbox without copying or rewriting their messages.
CREATE TABLE social_profiles (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 handle text NOT NULL UNIQUE CHECK(handle ~ '^[a-z][a-z0-9_]{2,35}$'),
 discoverable boolean NOT NULL DEFAULT false,
 connection_promotions boolean NOT NULL DEFAULT false,
 phone_e164 text UNIQUE,
 phone_verified_at timestamptz,
 town_hall_seen_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE social_connections (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_low uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 user_high uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 requested_by uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','accepted','declined')),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(user_low<user_high), CHECK(requested_by IN (user_low,user_high)),
 UNIQUE(user_low,user_high)
);
CREATE INDEX social_connections_low ON social_connections(user_low,state,updated_at DESC);
CREATE INDEX social_connections_high ON social_connections(user_high,state,updated_at DESC);
CREATE TABLE social_blocks (
 blocker_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 blocked_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(blocker_id,blocked_id), CHECK(blocker_id<>blocked_id)
);
CREATE TABLE chat_rooms (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 kind text NOT NULL CHECK(kind IN ('direct','town_hall')),
 owner_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 direct_low uuid REFERENCES users(id) ON DELETE RESTRICT,
 direct_high uuid REFERENCES users(id) ON DELETE RESTRICT,
 title text NOT NULL DEFAULT '',
 context jsonb NOT NULL DEFAULT '{}',
 state text NOT NULL DEFAULT 'open' CHECK(state IN ('open','closed')),
 revision bigint NOT NULL DEFAULT 0,
 last_message_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now(),
 closed_at timestamptz,
 CHECK ((kind='direct' AND direct_low IS NOT NULL AND direct_high IS NOT NULL AND direct_low<direct_high)
     OR (kind='town_hall' AND direct_low IS NULL AND direct_high IS NULL))
);
CREATE UNIQUE INDEX chat_direct_pair ON chat_rooms(direct_low,direct_high) WHERE kind='direct';
CREATE INDEX chat_room_activity ON chat_rooms(last_message_at DESC,id DESC);
CREATE TABLE chat_participants (
 room_id uuid NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 provider_id uuid REFERENCES provider_organizations(id) ON DELETE RESTRICT,
 role text NOT NULL CHECK(role IN ('member','buyer','provider')),
 state text NOT NULL DEFAULT 'active' CHECK(state IN ('active','removed')),
 joined_at timestamptz NOT NULL DEFAULT now(),
 last_read_at timestamptz,
 PRIMARY KEY(room_id,user_id),
 CHECK((role='provider')=(provider_id IS NOT NULL))
);
CREATE UNIQUE INDEX chat_provider_seat ON chat_participants(room_id,provider_id)
 WHERE provider_id IS NOT NULL AND state='active';
CREATE INDEX chat_member_inbox ON chat_participants(user_id,state,room_id);
CREATE TABLE chat_messages (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 room_id uuid NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
 sender_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 body text NOT NULL CHECK(char_length(body) BETWEEN 1 AND 2000),
 media_keys text[] NOT NULL DEFAULT '{}',
 client_message_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(sender_user_id,client_message_id)
);
CREATE INDEX chat_message_history ON chat_messages(room_id,created_at DESC,id DESC);
CREATE TABLE town_hall_requests (
 room_id uuid PRIMARY KEY REFERENCES chat_rooms(id) ON DELETE CASCADE,
 description text NOT NULL CHECK(char_length(description) BETWEEN 1 AND 2000),
 category text NOT NULL DEFAULT '',
 location text NOT NULL,
 quantity numeric(14,3) NOT NULL CHECK(quantity>0),
 unit text NOT NULL,
 currency_code text NOT NULL DEFAULT 'NGN' CHECK(currency_code='NGN'),
 target_unit_price numeric(14,2) NOT NULL CHECK(target_unit_price>0)
);
ALTER TABLE chat_rooms ADD COLUMN client_request_id uuid;
CREATE UNIQUE INDEX town_hall_create_retry ON chat_rooms(owner_user_id,client_request_id) WHERE kind='town_hall';
CREATE INDEX town_hall_activity ON chat_rooms(last_message_at DESC,id) WHERE kind='town_hall' AND state='open';
CREATE TABLE town_hall_applications (
 room_id uuid NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
 provider_id uuid NOT NULL REFERENCES provider_organizations(id) ON DELETE RESTRICT,
 requester_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','admitted','declined','removed')),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(room_id,provider_id)
);
CREATE INDEX town_hall_applicants ON town_hall_applications(room_id,state,created_at);
CREATE TABLE town_hall_offers (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 room_id uuid NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
 provider_id uuid NOT NULL REFERENCES provider_organizations(id) ON DELETE RESTRICT,
 sender_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 unit_price numeric(14,2) NOT NULL CHECK(unit_price>0),
 terms text NOT NULL DEFAULT '' CHECK(char_length(terms)<=1000),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(room_id,provider_id)
);
CREATE INDEX town_hall_offers_room ON town_hall_offers(room_id,unit_price,id);
CREATE TABLE chat_reports (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 reporter_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 room_id uuid REFERENCES chat_rooms(id) ON DELETE RESTRICT,
 legacy_conversation_id uuid REFERENCES provider_conversations(id) ON DELETE RESTRICT,
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 1 AND 1000),
 state text NOT NULL DEFAULT 'open' CHECK(state IN ('open','resolved')),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((room_id IS NOT NULL)::int+(legacy_conversation_id IS NOT NULL)::int=1)
);
CREATE OR REPLACE FUNCTION enforce_town_hall_seats() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE room_kind text; room_state text; seats integer;
BEGIN
 IF NEW.role<>'provider' OR NEW.state<>'active' THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND OLD.state='active' AND OLD.provider_id=NEW.provider_id AND OLD.room_id=NEW.room_id THEN RETURN NEW; END IF;
 SELECT kind,state INTO room_kind,room_state FROM chat_rooms WHERE id=NEW.room_id FOR UPDATE;
 IF room_kind<>'town_hall' THEN RAISE EXCEPTION 'Provider seats require a Town Hall room'; END IF;
 IF room_state<>'open' THEN RAISE EXCEPTION 'Town Hall request is closed' USING ERRCODE='23514'; END IF;
 SELECT count(*) INTO seats FROM chat_participants WHERE room_id=NEW.room_id AND role='provider' AND state='active';
 IF seats>=10 THEN RAISE EXCEPTION 'Town Hall is full' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER town_hall_seat_limit BEFORE INSERT OR UPDATE OF state,provider_id,room_id ON chat_participants
 FOR EACH ROW EXECUTE FUNCTION enforce_town_hall_seats();
