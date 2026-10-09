package migrations

import (
	"across/backend/internal/config"
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigrationLedgerCommitsWithSchemaAndSingleConnection(t *testing.T) {
	if os.Getenv("SETTLEMENT_TEST_LOCAL") != "true" {
		t.Skip("requires opt-in loopback PostgreSQL")
	}
	cwd, _ := os.Getwd()
	if err := os.Chdir(filepath.Join("..", "..")); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	_ = os.Chdir(cwd)
	parsed, err := url.Parse(cfg.DatabaseURL)
	if err != nil || (parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1") {
		t.Fatal("requires a loopback database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatal("local database unavailable")
	}
	defer admin.Close()
	schema := "migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	pc, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		t.Fatal("invalid local configuration")
	}
	pc.MaxConns = 1
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	if err = os.Mkdir(filepath.Join(dir, "migrations"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "migrations", "001_fixture.sql"), []byte("CREATE TABLE atomic_fixture(id int PRIMARY KEY); INSERT INTO atomic_fixture VALUES (1);"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	_, err = db.Exec(ctx, `CREATE TABLE schema_migrations(name text PRIMARY KEY,applied_at timestamptz);
 CREATE FUNCTION reject_migration_record() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'ledger unavailable'; END; $$;
 CREATE TRIGGER reject_record BEFORE INSERT ON schema_migrations FOR EACH ROW EXECUTE FUNCTION reject_migration_record();`)
	if err != nil {
		t.Fatal(err)
	}
	if err = Run(ctx, db); err == nil {
		t.Fatal("expected rejected ledger insert")
	}
	var exists bool
	if err = db.QueryRow(ctx, "SELECT to_regclass('atomic_fixture') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatalf("schema must roll back with rejected ledger: exists=%t, error=%v", exists, err)
	}
	if _, err = db.Exec(ctx, "DROP TRIGGER reject_record ON schema_migrations"); err != nil {
		t.Fatal(err)
	}
	if err = Run(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = Run(ctx, db); err != nil {
		t.Fatalf("non-repeatable migration was rerun: %v", err)
	}
	var count int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE name='001_fixture.sql'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected one applied record: count=%d, error=%v", count, err)
	}
	// Separate replicas have separate connection pools; the advisory lock must
	// serialize their non-repeatable schema changes, not just pool acquisition.
	second, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = os.WriteFile(filepath.Join(dir, "migrations", "002_concurrent.sql"), []byte("CREATE TABLE concurrent_fixture(id int PRIMARY KEY);"), 0600); err != nil {
		t.Fatal(err)
	}
	// Concurrent startup passes serialize under the advisory lock.
	errors := make(chan error, 2)
	go func() { errors <- Run(ctx, db) }()
	go func() { errors <- Run(ctx, second) }()
	for i := 0; i < 2; i++ {
		if err = <-errors; err != nil {
			t.Fatal(err)
		}
	}
}
