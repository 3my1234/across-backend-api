package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestReferralGoogleAccountContinuityAndRestrictions(t *testing.T) {
	db, existing, _ := referralFixture(t)
	ctx := context.Background()
	service := NewIdentityService(db)
	provider := uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO provider_organizations(id,owner_user_id) VALUES($1,$2)`, provider, existing); err != nil {
		t.Fatal(err)
	}
	user, created, err := service.ResolvePrivy(ctx, uuid.NewString(), "trusted-subject", " INVITER@example.test ", "Google name")
	if err != nil || created || user != existing {
		t.Fatalf("existing provider identity lost: %s %v %v", user, created, err)
	}
	again, created, err := service.ResolvePrivy(ctx, uuid.NewString(), "trusted-subject", "inviter@example.test", "Changed name")
	if err != nil || created || again != existing {
		t.Fatalf("repeat login: %v", err)
	}
	expect := func(subject, email string, status int) {
		t.Helper()
		_, _, failure := service.ResolvePrivy(ctx, uuid.NewString(), subject, email, "Google name")
		var apiError *fiber.Error
		if !errors.As(failure, &apiError) || apiError.Code != status {
			t.Fatalf("expected %d, got %v", status, failure)
		}
	}
	expect("different-subject", "inviter@example.test", 409)
	expect("trusted-subject", "different@example.test", 409)
	if _, err = db.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, existing); err != nil {
		t.Fatal(err)
	}
	expect("trusted-subject", "inviter@example.test", 403)
	if _, err = db.Exec(ctx, `INSERT INTO users(id,email,is_active,email_verified) VALUES($1,'suspended@example.test',false,true)`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	expect("new-subject", "suspended@example.test", 403)
	var active bool
	var email, owner string
	var identities int
	if err = db.QueryRow(ctx, `SELECT is_active,email,(SELECT owner_user_id::text FROM provider_organizations WHERE id=$2),(SELECT count(*) FROM user_identities WHERE user_id=$1) FROM users WHERE id=$1`, existing, provider).Scan(&active, &email, &owner, &identities); err != nil || active || email != "inviter@example.test" || owner != existing || identities != 1 {
		t.Fatalf("identity overwritten/reactivated: %v %s %s %d %v", active, email, owner, identities, err)
	}
}
