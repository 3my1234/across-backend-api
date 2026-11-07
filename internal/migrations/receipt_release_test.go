package migrations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Disposable schema only: prove actual migration DDL/ledger rollback and the
// previous worker's explicit-column query shape after additive migration.
// This does not establish production table size, lock duration or mixed-worker safety.
func TestReceiptMigrationReleaseRollback(t *testing.T) {
	if os.Getenv("MIGRATION_REVIEW_LOCAL") != "true" {
		t.Skip("explicit disposable migration review only")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "127.0.0.1" && cfg.Host != "localhost" && cfg.Host != "::1" {
		t.Fatal("loopback disposable PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	schema := "receipt_review_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, e := conn.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); e != nil {
			t.Errorf("disposable schema cleanup: %v", e)
		}
	}()
	if _, err = conn.Exec(ctx, "SET search_path TO "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join("..", ".."))
	if err = ensureTrackingTable(ctx, conn); err != nil {
		t.Fatal(err)
	}
	// Existing worker columns; foreign keys are immaterial to this DDL compatibility test.
	_, err = conn.Exec(ctx, `CREATE TABLE notification_push_deliveries (
	 notification_id UUID NOT NULL, push_token_id UUID NOT NULL,
	 status TEXT NOT NULL DEFAULT 'pending', expo_ticket_id TEXT,
	 sent_at TIMESTAMPTZ, receipt_checked_at TIMESTAMPTZ, updated_at TIMESTAMPTZ DEFAULT now(),
	 PRIMARY KEY(notification_id,push_token_id));
	 INSERT INTO notification_push_deliveries(notification_id,push_token_id,status,expo_ticket_id,sent_at)
	 VALUES(gen_random_uuid(),gen_random_uuid(),'sent','synthetic-ticket',now()-interval '20 minutes');
	 CREATE TABLE idx_push_receipt_claim_due (conflict BOOLEAN)`)
	if err != nil {
		t.Fatal(err)
	}
	const name = "108_push_receipt_claims.sql"
	reader, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close(context.Background())
	if _, err = reader.Exec(ctx, "BEGIN; SELECT count(*) FROM "+quoted+".notification_push_deliveries"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "SET lock_timeout='100ms'; SET statement_timeout='5s'"); err != nil {
		t.Fatal(err)
	}
	err = applyMigration(ctx, conn, name)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
		t.Fatalf("expected bounded lock refusal, got %v", err)
	}
	if _, err = reader.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	// Force failure after ALTER, proving partial columns and ledger do not commit.
	err = applyMigration(ctx, conn, name)
	if !errors.As(err, &pgerr) || pgerr.Code != "42P07" {
		t.Fatalf("expected duplicate relation fault, got %v", err)
	}
	var columns, ledger int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='notification_push_deliveries' AND column_name LIKE 'receipt_%' AND column_name!='receipt_checked_at'`, schema).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE name=$1", name).Scan(&ledger); err != nil || columns != 0 || ledger != 0 {
		t.Fatalf("partial migration committed: columns=%d ledger=%d err=%v", columns, ledger, err)
	}
	if _, err = conn.Exec(ctx, "DROP TABLE idx_push_receipt_claim_due"); err != nil {
		t.Fatal(err)
	}
	if err = applyMigration(ctx, conn, name); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE name=$1", name).Scan(&ledger); err != nil || ledger != 1 {
		t.Fatalf("migration ledger=%d err=%v", ledger, err)
	}
	var attempts, dispatch int
	if err = conn.QueryRow(ctx, "SELECT receipt_attempts FROM notification_push_deliveries").Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("existing row defaults: attempts=%d err=%v", attempts, err)
	}
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM push_receipt_dispatch WHERE singleton").Scan(&dispatch); err != nil || dispatch != 1 {
		t.Fatalf("dispatch gate=%d err=%v", dispatch, err)
	}
	// Exact old receipt selection and success update remain usable after DDL.
	var notification, token, ticket string
	err = conn.QueryRow(ctx, `SELECT notification_id::text,push_token_id::text,expo_ticket_id
	 FROM notification_push_deliveries WHERE status='sent' AND receipt_checked_at IS NULL
	 AND expo_ticket_id IS NOT NULL AND sent_at<=now()-interval '15 minutes'
	 ORDER BY sent_at,notification_id,push_token_id LIMIT $1`, 100).Scan(&notification, &token, &ticket)
	if err != nil || ticket != "synthetic-ticket" {
		t.Fatalf("old worker selection: %v", err)
	}
	if _, err = conn.Exec(ctx, `UPDATE notification_push_deliveries SET status='delivered',receipt_checked_at=now(),updated_at=now() WHERE notification_id=$1::uuid AND push_token_id=$2::uuid`, notification, token); err != nil {
		t.Fatal(err)
	}
}
