package services

import (
	"across/backend/internal/config"
	"context"
	"testing"
)

// Uses the explicitly enabled loopback-only isolated PostgreSQL fixture.
// SMTP is deliberately unconfigured; no message can be sent.
func TestVerificationClaimedBeforeChatBacklogWithoutStarvation(t *testing.T) {
	db := receiptTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,email text,full_name text);
 CREATE TABLE email_suppressions(email text PRIMARY KEY);
 CREATE TABLE email_outbox(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid,dedupe_key text UNIQUE,recipient_email text,recipient_name text DEFAULT '',template_type text,payload jsonb DEFAULT '{}',status text DEFAULT 'pending',attempts integer DEFAULT 0,next_attempt_at timestamptz DEFAULT now(),locked_at timestamptz,last_error text DEFAULT '',updated_at timestamptz DEFAULT now(),created_at timestamptz DEFAULT now(),sent_at timestamptz);
 INSERT INTO email_outbox(dedupe_key,recipient_email,template_type,created_at,next_attempt_at)
 SELECT 'chat:'||n,'test@example.invalid','marketplace_activity',now()-interval '1 hour',now()-interval '1 hour' FROM generate_series(1,500) n;
 INSERT INTO email_outbox(dedupe_key,recipient_email,template_type) VALUES('verification','test@example.invalid','verification');`)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = RunEmailDeliveryBatch(ctx, db, NewEmailService(config.Config{}))
	var verified, normal int
	err = db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE dedupe_key='verification' AND attempts=1),count(*) FILTER (WHERE template_type='marketplace_activity' AND attempts=1) FROM email_outbox`).Scan(&verified, &normal)
	if err != nil || verified != 1 || normal < 1 {
		t.Fatalf("verification=%d normal=%d err=%v", verified, normal, err)
	}
}
