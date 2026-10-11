package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestAdminFreePaidFreeTransitions(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_subscription_plans(amount_ngn numeric,is_active boolean);
 INSERT INTO provider_subscription_plans VALUES(500,true),(1000,true),(2000,true);
 CREATE TABLE provider_subscription_access(singleton boolean PRIMARY KEY,enforced boolean,start_at timestamptz,updated_at timestamptz DEFAULT now(),updated_by uuid);
 INSERT INTO provider_subscription_access(singleton,enforced) VALUES(true,false);
 CREATE TABLE provider_subscription_access_events(admin_id uuid,old_enforced boolean,old_start_at timestamptz,new_enforced boolean,new_start_at timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{ProviderSubscriptionsEnforced: true}
	policy := config.NewProviderSubscriptionPolicy(db, cfg)
	cfg.ProviderSubscriptionPolicy = policy
	replica := config.NewProviderSubscriptionPolicy(db, cfg)
	controller := &ProviderMarketplaceController{db: db, cfg: cfg}
	app := fiber.New()
	app.Put("/access", func(c *fiber.Ctx) error {
		c.Locals("admin_id", uuid.New().String())
		return controller.AdminSetProviderSubscriptionAccess(c)
	})
	set := func(body string, status int) config.ProviderSubscriptionAccess {
		t.Helper()
		req := httptest.NewRequest("PUT", "/access", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("%s: status %d, expected %d", body, res.StatusCode, status)
		}
		var access config.ProviderSubscriptionAccess
		if status == 200 {
			if err := json.NewDecoder(res.Body).Decode(&access); err != nil {
				t.Fatal(err)
			}
		}
		return access
	}
	for _, enforced := range []bool{false, true, false, true, false} {
		body := `{"enforced":false}`
		if enforced {
			body = `{"enforced":true}`
		}
		access := set(body, 200)
		if access.Enforced != enforced || access.Required != enforced || access.Source != "admin" || access.StartAt != nil {
			t.Fatalf("wrong saved access: %+v", access)
		}
		if err := replica.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		if replica.Required(time.Now()) != enforced {
			t.Fatal("second API instance did not read saved policy")
		}
	}
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	access := set(`{"enforced":true,"start_at":"`+future.Format(time.RFC3339)+`"}`, 200)
	if access.Required || !policy.Required(future) {
		t.Fatal("scheduled paid access boundary incorrect")
	}
	access = set(`{"enforced":false}`, 200)
	if access.StartAt != nil || policy.Required(future) {
		t.Fatal("free access must cancel scheduled paid access")
	}
	set(`{"enforced":true,"start_at":"invalid"}`, 400)
	if _, err := db.Exec(ctx, `UPDATE provider_subscription_plans SET is_active=false`); err != nil {
		t.Fatal(err)
	}
	set(`{"enforced":true}`, 409)
	// Restoring free access remains possible even with no plans or gateway available.
	access = set(`{"enforced":false}`, 200)
	if access.Required {
		t.Fatal("failed to restore free access")
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM provider_subscription_access_events`).Scan(&count); err != nil || count != 8 {
		t.Fatalf("audit count %d: %v", count, err)
	}
}
