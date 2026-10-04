package controllers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"across/backend/internal/config"
	"github.com/google/uuid"
)

func TestProviderSubscriptionAccessMigrationAndAudit(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `
		CREATE TABLE admins(id UUID PRIMARY KEY);
		CREATE TABLE provider_subscription_plans(id UUID PRIMARY KEY,amount_ngn NUMERIC(14,2) NOT NULL);
		CREATE TABLE provider_subscriptions(id UUID PRIMARY KEY,plan_id UUID NOT NULL);
		CREATE TABLE provider_subscription_payments(subscription_id UUID NOT NULL,amount NUMERIC(14,2) NOT NULL,paid_at TIMESTAMPTZ NOT NULL);
	`)
	if err != nil {
		t.Fatal(err)
	}
	adminID, planID, oldID := uuid.New(), uuid.New(), uuid.New()
	for _, entry := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO admins VALUES($1)`, []any{adminID}},
		{`INSERT INTO provider_subscription_plans VALUES($1,500)`, []any{planID}},
		{`INSERT INTO provider_subscriptions VALUES($1,$2)`, []any{oldID, planID}},
		{`INSERT INTO provider_subscription_payments VALUES($1,100,now())`, []any{oldID}},
	} {
		if _, err = db.Exec(ctx, entry.query, entry.args...); err != nil {
			t.Fatal(err)
		}
	}
	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "053_provider_subscription_access.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	var originalAmount float64
	if err = db.QueryRow(ctx, `SELECT expected_amount_ngn FROM provider_subscriptions WHERE id=$1`, oldID).Scan(&originalAmount); err != nil || originalAmount != 100 {
		t.Fatalf("checkout amount backfill = %v, %v", originalAmount, err)
	}
	policy := config.NewProviderSubscriptionPolicy(db, config.Config{ProviderSubscriptionsEnforced: true})
	if err = policy.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if access := policy.Current(time.Now()); access.Source != "environment" || !access.Required {
		t.Fatalf("unexpected default access: %+v", access)
	}
	if err = policy.Set(ctx, false, nil, adminID.String()); err != nil {
		t.Fatal(err)
	}
	if err = policy.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if access := policy.Current(time.Now()); access.Source != "admin" || access.Required {
		t.Fatalf("unexpected free access: %+v", access)
	}
	var events int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM provider_subscription_access_events WHERE admin_id=$1 AND new_enforced=FALSE`, adminID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("access audit events = %d, %v", events, err)
	}
}
