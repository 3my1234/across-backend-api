package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitSQLStatementsPreservesDollarQuotedFunction(t *testing.T) {
	input := `CREATE OR REPLACE FUNCTION test_trigger()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE products SET review_count = review_count + 1;
  RETURN NEW;
END;
$$;
CREATE TRIGGER test_trigger AFTER INSERT ON reviews
FOR EACH ROW EXECUTE FUNCTION test_trigger();`

	statements := splitSQLStatements(input)
	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d: %#v", len(statements), statements)
	}
	if !strings.Contains(statements[0], "RETURN NEW;\nEND;\n$$") {
		t.Fatalf("function body was split or altered: %q", statements[0])
	}
}

func TestSplitSQLStatementsHandlesQuotesAndComments(t *testing.T) {
	input := `-- comment containing ;
INSERT INTO example(value) VALUES ('semi;colon and it''s valid');
/* outer ; /* nested ; */ still outer */
INSERT INTO example(value) VALUES ("quoted;identifier");`

	statements := splitSQLStatements(input)
	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d: %#v", len(statements), statements)
	}
}

func TestMigration032SplitsIntoCompleteStatements(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "migrations", "032_review_aggregates_and_support_activity.sql"))
	if err != nil {
		t.Fatal(err)
	}

	statements := splitSQLStatements(string(content))
	if len(statements) != 7 {
		t.Fatalf("expected 7 complete statements, got %d", len(statements))
	}
	if !strings.Contains(statements[2], "CREATE OR REPLACE FUNCTION maintain_product_review_aggregates()") ||
		!strings.Contains(statements[2], "RETURN OLD;\nEND;\n$$") {
		t.Fatalf("review aggregate function was not kept intact: %q", statements[2])
	}
}

func TestMigration034KeepsProviderIntegrityRepairComplete(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "migrations", "034_provider_marketplace_integrity.sql"))
	if err != nil {
		t.Fatal(err)
	}

	statements := splitSQLStatements(string(content))
	if len(statements) != 4 {
		t.Fatalf("expected 4 complete statements, got %d", len(statements))
	}
	if !strings.Contains(statements[0], "reviewed_by UUID REFERENCES admins(id)") ||
		!strings.Contains(statements[1], "actor_user_id UUID REFERENCES users(id)") ||
		!strings.Contains(statements[3], "ALTER COLUMN event_type SET NOT NULL") {
		t.Fatalf("provider integrity migration is incomplete: %#v", statements)
	}
}
