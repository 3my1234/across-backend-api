-- Preserve every original code and attribution. Custom names occupy the same
-- namespace as originals, so another account can never claim an old invitation.
CREATE TABLE referral_code_names (
 code TEXT PRIMARY KEY CHECK (code ~ '^[A-Z0-9]{4,20}$'),
 user_id UUID NOT NULL REFERENCES referral_codes(user_id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK (kind IN ('original','custom')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,kind)
);
CREATE FUNCTION register_original_referral_code() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO referral_code_names(code,user_id,kind) VALUES(NEW.code,NEW.user_id,'original')
 ON CONFLICT(user_id,kind) DO NOTHING;
 RETURN NEW;
END;
$$;
-- Install before backfill to cover issuance by an older API during rollout.
CREATE TRIGGER register_original_referral_code AFTER INSERT ON referral_codes
 FOR EACH ROW EXECUTE FUNCTION register_original_referral_code();
INSERT INTO referral_code_names(code,user_id,kind)
 SELECT code,user_id,'original' FROM referral_codes ON CONFLICT(user_id,kind) DO NOTHING;
