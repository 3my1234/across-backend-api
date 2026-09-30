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

func TestMigration042KeepsImmutablePaymentTriggerComplete(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "migrations", "042_provider_independent_payments.sql"))
	if err != nil {
		t.Fatal(err)
	}
	statements := splitSQLStatements(string(content))
	joined := strings.Join(statements, "\n")
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS payments",
		"CREATE TABLE IF NOT EXISTS payment_webhook_events",
		"CREATE OR REPLACE FUNCTION protect_payment_financial_identity()",
		"RAISE EXCEPTION 'payment financial identity is immutable'",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("payment foundation migration is missing %q", required)
		}
	}
}

func TestMigration046DefinesCompleteAccountDeletionPolicy(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "migrations", "046_account_hard_delete_cascades.sql"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(splitSQLStatements(string(content)), "\n")
	for _, required := range []string{
		"orders_user_id_fkey",
		"provider_organizations_owner_user_id_fkey",
		"payments_provider_subscription_id_fkey",
		"payments_user_id_fkey",
		"products_provider_id_fkey",
		"provider_conversations_user_id_fkey",
		"ON DELETE CASCADE",
		"ON DELETE SET NULL",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("account deletion migration is missing %q", required)
		}
	}
}

func TestMigration047AddsRegistrationContext(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "migrations", "047_registration_context.sql"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(splitSQLStatements(string(content)), "\n")
	for _, required := range []string{"registration_context", "'buyer'", "'provider'"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("registration context migration is missing %q", required)
		}
	}
}
