package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestMigrationLockWaitIsBoundedAndPreservesExistingWrites(t *testing.T) {
	if os.Getenv("MIGRATION_GATE_LAB") != "isolated-ci" {
		t.Skip("isolated migration fixture only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	raw := os.Getenv("MIGRATION_GATE_DATABASE_URL")
	parsed, err := pgconn.ParseConfig(raw)
	if err != nil || parsed.Database != "migration_gate_fixture" {
		t.Fatal("Only fixed synthetic database is allowed")
	}
	business, err := NewPostgresPool(ctx, raw, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer business.Close()
	if _, err = business.Exec(ctx, `CREATE TABLE migration_lock_fixture(id bigint PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	defer business.Exec(context.Background(), `DROP TABLE migration_lock_fixture`)
	writer, err := business.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	if _, err = writer.Exec(ctx, `INSERT INTO migration_lock_fixture VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	migrator, err := NewMigrationPool(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer migrator.Close()
	began := time.Now()
	_, err = migrator.Exec(ctx, `ALTER TABLE migration_lock_fixture ADD COLUMN candidate text`)
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "55P03" || time.Since(began) > 6*time.Second {
		t.Fatalf("Expected bounded lock refusal, got %v after %v", err, time.Since(began))
	}
	if err = writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = business.QueryRow(ctx, `SELECT count(*) FROM migration_lock_fixture`).Scan(&count); err != nil || count != 1 {
		t.Fatal("Existing write lost")
	}
	if _, err = migrator.Exec(ctx, `ALTER TABLE migration_lock_fixture ADD COLUMN candidate text`); err != nil {
		t.Fatal("Migration did not recover after lock release", err)
	}
}
