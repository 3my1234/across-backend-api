package services

import (
	"across/backend/internal/config"
	"context"
	"testing"
)

func TestChatOutboxSuppressedWithoutSuppressingOtherEmail(t *testing.T) {
	db := receiptTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,email text,full_name text);
 CREATE TABLE email_suppressions(email text PRIMARY KEY);
 CREATE TABLE email_outbox(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid,dedupe_key text UNIQUE,recipient_email text,recipient_name text DEFAULT '',template_type text,payload jsonb DEFAULT '{}',status text DEFAULT 'pending',attempts integer DEFAULT 0,next_attempt_at timestamptz DEFAULT now(),locked_at timestamptz,last_error text DEFAULT '',updated_at timestamptz DEFAULT now(),created_at timestamptz DEFAULT now(),sent_at timestamptz);
 INSERT INTO email_outbox(dedupe_key,recipient_email,template_type,status) VALUES
 ('provider-message:one','test@example.invalid','marketplace_activity','pending'),
	 ('provider-message:two','test@example.invalid','marketplace_activity','retry'),
	 ('provider-request:one','test@example.invalid','marketplace_activity','pending'),
	 ('provider-request-email:one','test@example.invalid','marketplace_activity','pending'),
	 ('listing-submitted:one','test@example.invalid','marketplace_activity','pending'),
	 ('merchant-product-submitted:one','test@example.invalid','marketplace_activity','pending'),
	 ('welcome:one','test@example.invalid','welcome','pending'),
	 ('listing-moderation:00000000-0000-0000-0000-000000000001:approved','test@example.invalid','marketplace_activity','pending'),
	 ('listing-moderation:00000000-0000-0000-0000-000000000001:rejected','test@example.invalid','marketplace_activity','pending'),
	 ('product-moderation:00000000-0000-0000-0000-000000000001:approved','test@example.invalid','marketplace_activity','pending'),
	 ('provider-verification:00000000-0000-0000-0000-000000000001:rejected','test@example.invalid','marketplace_activity','pending'),
	 ('other:one','test@example.invalid','marketplace_activity','pending'),
	 ('password-reset:00000000-0000-0000-0000-000000000001:digest','test@example.invalid','password_reset','pending'),
	 ('verification:one','test@example.invalid','verification','pending');`)
	if err != nil {
		t.Fatal(err)
	}
	// No SMTP credentials: non-chat messages fail safely into retry without sending.
	_, _ = RunEmailDeliveryBatch(ctx, db, NewEmailService(config.Config{}))
	var suppressed, otherAttempted int
	err = db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='suppressed' AND attempts=0), count(*) FILTER (WHERE status='retry' AND attempts=1 AND last_error LIKE '%SMTP%') FROM email_outbox`).Scan(&suppressed, &otherAttempted)
	if err != nil || suppressed != 7 || otherAttempted != 7 {
		t.Fatalf("suppressed=%d otherAttempted=%d err=%v", suppressed, otherAttempted, err)
	}
}
