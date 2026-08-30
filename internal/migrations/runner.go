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
	for i, name := range names {
		if name == "schema.sql" {
			if i > 0 {
				names = append([]string{name}, append(names[:i], names[i+1:]...)...)
			}
			break
		}
	}
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
	var builder strings.Builder
	statements := make([]string, 0)
	var dollarTag string
	inSingleQuote := false
	inDoubleQuote := false
	inLineComment := false
	blockCommentDepth := 0

	flush := func() {
		if statement := strings.TrimSpace(builder.String()); statement != "" {
			statements = append(statements, statement)
		}
		builder.Reset()
	}

	for i := 0; i < len(input); i++ {
		ch := input[i]

		if inLineComment {
			builder.WriteByte(ch)
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}

		if blockCommentDepth > 0 {
			builder.WriteByte(ch)
			if ch == '/' && i+1 < len(input) && input[i+1] == '*' {
				builder.WriteByte(input[i+1])
				i++
				blockCommentDepth++
				continue
			}
			if ch == '*' && i+1 < len(input) && input[i+1] == '/' {
				builder.WriteByte(input[i+1])
				i++
				blockCommentDepth--
			}
			continue
		}

		if dollarTag != "" {
			if strings.HasPrefix(input[i:], dollarTag) {
				builder.WriteString(dollarTag)
				i += len(dollarTag) - 1
				dollarTag = ""
			} else {
				builder.WriteByte(ch)
			}
			continue
		}

		if inSingleQuote {
			builder.WriteByte(ch)
			if ch == '\\' && i+1 < len(input) {
				builder.WriteByte(input[i+1])
				i++
				continue
			}
			if ch == '\'' {
				if i+1 < len(input) && input[i+1] == '\'' {
					builder.WriteByte(input[i+1])
					i++
				} else {
					inSingleQuote = false
				}
			}
			continue
		}

		if inDoubleQuote {
			builder.WriteByte(ch)
			if ch == '"' {
				if i+1 < len(input) && input[i+1] == '"' {
					builder.WriteByte(input[i+1])
					i++
				} else {
					inDoubleQuote = false
				}
			}
			continue
		}

		switch ch {
		case '\'':
			inSingleQuote = true
			builder.WriteByte(ch)
		case '"':
			inDoubleQuote = true
			builder.WriteByte(ch)
		case '-':
			builder.WriteByte(ch)
			if i+1 < len(input) && input[i+1] == '-' {
				builder.WriteByte(input[i+1])
				i++
				inLineComment = true
			}
		case '/':
			builder.WriteByte(ch)
			if i+1 < len(input) && input[i+1] == '*' {
				builder.WriteByte(input[i+1])
				i++
				blockCommentDepth = 1
			}
		case '$':
			if tag, ok := scanDollarTag(input[i:]); ok {
				dollarTag = tag
				builder.WriteString(tag)
				i += len(tag) - 1
			} else {
				builder.WriteByte(ch)
			}
		case ';':
			flush()
		default:
			builder.WriteByte(ch)
		}
	}
	flush()
	return statements
}

func scanDollarTag(input string) (string, bool) {
	if len(input) < 2 || input[0] != '$' {
		return "", false
	}
	for i := 1; i < len(input); i++ {
		ch := input[i]
		if ch == '$' {
			return input[:i+1], true
		}
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return "", false
		}
	}
	return "", false
}

func coreSchemaExists(ctx context.Context, db *pgxpool.Pool) (bool, error) {
	var exists bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('public.countries_config') IS NOT NULL`).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}
