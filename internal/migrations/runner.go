package migrations

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Run(ctx context.Context, db *pgxpool.Pool) error {
	if err := ensureTrackingTable(ctx, db); err != nil {
		return err
	}

	files, err := loadMigrationNames()
	if err != nil {
		return err
	}

	applied, err := appliedMigrations(ctx, db)
	if err != nil {
		return err
	}

	hasBaseline, err := coreSchemaExists(ctx, db)
	if err != nil {
		return err
	}

	for _, name := range files {
		if _, ok := applied[name]; ok {
			continue
		}

		if name == "schema.sql" && hasBaseline {
			if err := markApplied(ctx, db, name); err != nil {
				return err
			}
			log.Printf("migration marked as applied: %s", name)
			continue
		}

		if err := applyMigration(ctx, db, name); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if err := markApplied(ctx, db, name); err != nil {
			return err
		}
		log.Printf("migration applied: %s", name)
	}

	return nil
}

func ensureTrackingTable(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	return err
}

func appliedMigrations(ctx context.Context, db *pgxpool.Pool) (map[string]struct{}, error) {
	rows, err := db.Query(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		applied[name] = struct{}{}
	}
	return applied, rows.Err()
}

func markApplied(ctx context.Context, db *pgxpool.Pool, name string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO schema_migrations(name, applied_at)
		VALUES ($1, $2)
		ON CONFLICT (name) DO NOTHING
	`, name, time.Now().UTC())
	return err
}

func loadMigrationNames() ([]string, error) {
	entries, err := os.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func applyMigration(ctx context.Context, db *pgxpool.Pool, name string) error {
	content, err := os.ReadFile(filepath.Join("migrations", name))
	if err != nil {
		return err
	}
	statements := splitSQLStatements(string(content))
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, statement := range statements {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func splitSQLStatements(input string) []string {
	lines := strings.Split(input, "\n")
	var builder strings.Builder
	statements := make([]string, 0)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		builder.WriteString(line)
		builder.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			statement := strings.TrimSpace(builder.String())
			statement = strings.TrimSuffix(statement, ";")
			if statement != "" {
				statements = append(statements, statement)
			}
			builder.Reset()
		}
	}
	if tail := strings.TrimSpace(builder.String()); tail != "" {
		statements = append(statements, tail)
	}
	return statements
}

func coreSchemaExists(ctx context.Context, db *pgxpool.Pool) (bool, error) {
	var exists bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('public.countries_config') IS NOT NULL`).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}
