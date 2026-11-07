package controllers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestRenewalOperationFencingAndRecovery(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE provider_renewal_preferences(provider_id uuid PRIMARY KEY,renewal_status text,next_plan_id uuid,change_at timestamptz,updated_at timestamptz DEFAULT now());
 CREATE TABLE provider_renewal_cancellations(provider_id uuid,status text,updated_at timestamptz DEFAULT now());
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,is_active boolean,role text);`)
	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "114_provider_renewal_operation_lease.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(string(sql))
	provider, other, owner, oldToken, newToken := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO provider_renewal_preferences(provider_id,renewal_status,operation_token,operation_expires_at) VALUES($1,'uncertain',$2,clock_timestamp()-interval '1 second'),($3,'uncertain',NULL,NULL)`, provider, oldToken, other)
	exec(`INSERT INTO provider_members VALUES($1,$2,true,'owner')`, provider, owner)
	exec(`INSERT INTO provider_renewal_cancellations(provider_id,status) VALUES($1,'requested')`, provider)
	// A crashed operation expires; replacement ownership cannot be stolen by its
	// late result or cleanup. Another provider's preferences remain independent.
	exec(`UPDATE provider_renewal_preferences SET operation_token=$2,operation_expires_at=clock_timestamp()+interval '2 minutes' WHERE provider_id=$1`, provider, newToken)
	releaseRenewalOperation(db, provider, oldToken)
	var retained string
	if err = db.QueryRow(ctx, `SELECT operation_token::text FROM provider_renewal_preferences WHERE provider_id=$1`, provider).Scan(&retained); err != nil || retained != newToken {
		t.Fatalf("stale cleanup released replacement: %s %v", retained, err)
	}
	if e := completeRenewalOperation(ctx, db, provider, owner, oldToken, nil, nil); e == nil || e.(*fiber.Error).Code != 409 {
		t.Fatalf("stale finalization accepted: %v", e)
	}
	exec(`UPDATE provider_renewal_preferences SET renewal_status='stopped' WHERE provider_id=$1`, other)
	// Ownership is checked again after external work; revocation must not confirm
	// the cancellation or rewrite entitlement preferences.
	exec(`UPDATE provider_members SET role='staff' WHERE provider_id=$1`, provider)
	if e := completeRenewalOperation(ctx, db, provider, owner, newToken, nil, nil); e == nil || e.(*fiber.Error).Code != 403 {
		t.Fatalf("revoked owner finalized: %v", e)
	}
	var status string
	if err = db.QueryRow(ctx, `SELECT status FROM provider_renewal_cancellations WHERE provider_id=$1`, provider).Scan(&status); err != nil || status != "requested" {
		t.Fatalf("failed finalization changed cancellation: %s %v", status, err)
	}
	exec(`UPDATE provider_members SET role='owner' WHERE provider_id=$1`, provider)
	// A local failure after updating cancellations rolls the entire final tx back.
	exec(`ALTER TABLE provider_renewal_preferences ADD CONSTRAINT fail_stopped CHECK(renewal_status<>'stopped') NOT VALID`)
	if e := completeRenewalOperation(ctx, db, provider, owner, newToken, nil, nil); e == nil {
		t.Fatal("injected final write failure accepted")
	}
	if err = db.QueryRow(ctx, `SELECT status FROM provider_renewal_cancellations WHERE provider_id=$1`, provider).Scan(&status); err != nil || status != "requested" {
		t.Fatal("partial confirmation escaped rollback")
	}
	exec(`ALTER TABLE provider_renewal_preferences DROP CONSTRAINT fail_stopped`)
	if e := completeRenewalOperation(ctx, db, provider, owner, newToken, nil, nil); e != nil {
		t.Fatal(e)
	}
	var cleared bool
	if err = db.QueryRow(ctx, `SELECT renewal_status,operation_token IS NULL AND operation_expires_at IS NULL FROM provider_renewal_preferences WHERE provider_id=$1`, provider).Scan(&status, &cleared); err != nil || status != "stopped" || !cleared {
		t.Fatalf("final state: %s %v %v", status, cleared, err)
	}
	// No active lease: normal deletion must not be suppressed by the fence trigger.
	exec(`DELETE FROM provider_renewal_preferences WHERE provider_id=$1`, provider)
}
