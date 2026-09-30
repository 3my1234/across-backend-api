CREATE OR REPLACE FUNCTION enqueue_notification_push_deliveries()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO notification_push_deliveries (notification_id, push_token_id)
    SELECT NEW.id, token.id
    FROM user_push_tokens token
    WHERE token.user_id = NEW.user_id
      AND token.disabled_at IS NULL
    ON CONFLICT (notification_id, push_token_id) DO NOTHING;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS notifications_enqueue_push_deliveries ON notifications;
CREATE TRIGGER notifications_enqueue_push_deliveries
AFTER INSERT ON notifications
FOR EACH ROW
EXECUTE FUNCTION enqueue_notification_push_deliveries();
