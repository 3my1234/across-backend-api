package controllers

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransportPlanPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, role, code string
		transport        bool
		amount           float64
		months           int
		ok               bool
	}{
		{"ordinary provider", "", "provider-monthly", false, 1000, 3, true},
		{"existing unclassified", "", "provider-business", true, 5000, 1, false},
		{"driver monthly", "driver", "provider-business", true, 5000, 1, true},
		{"driver yearly", "driver", "provider-business", true, 5000, 12, true},
		{"rider yearly", "rider", "provider-growth", true, 2500, 12, true},
		{"wrong tier", "rider", "provider-business", true, 5000, 1, false},
		{"wrong duration", "driver", "provider-business", true, 5000, 3, false},
		{"old price", "driver", "provider-business", true, 2000, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (transportPlanError(tc.transport, tc.role, tc.code, tc.amount, tc.months) == nil) != tc.ok {
				t.Fatal("unexpected transport eligibility")
			}
		})
	}
}

func TestTransportSelectionOwnershipAndExistingPaidAccess(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_organizations(id uuid PRIMARY KEY,is_active boolean DEFAULT true,is_driver boolean DEFAULT true,transport_role text DEFAULT '');
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean DEFAULT true);
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,code text,amount_ngn numeric,is_active boolean DEFAULT true);
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	provider, owner, staff, driverPlan, riderPlan := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO provider_organizations(id) VALUES($1)`, []any{provider}},
		{`INSERT INTO provider_members VALUES($1,$2,'owner',true),($1,$3,'staff',true)`, []any{provider, owner, staff}},
		{`INSERT INTO provider_subscription_plans VALUES($1,'provider-business',5000,true),($2,'provider-growth',2500,true)`, []any{driverPlan, riderPlan}},
		{`INSERT INTO provider_subscriptions VALUES($1,'active','2027-01-01T00:00:00Z')`, []any{provider}},
	} {
		if _, err = db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if checkTransportPlan(ctx, db, provider, driverPlan, 1) == nil {
		t.Fatal("unclassified profile must choose before a new purchase")
	}
	app := fiber.New()
	m := &ProviderMarketplaceController{db: db}
	actor := staff
	app.Post("/role", func(c *fiber.Ctx) error { c.Locals("user_id", actor); return m.SelectTransportRole(c) })
	request := func(role string) int {
		req := httptest.NewRequest("POST", "/role", strings.NewReader(`{"transport_role":"`+role+`"}`))
		req.Header.Set("Content-Type", "application/json")
		r, e := app.Test(req)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		return r.StatusCode
	}
	if request("rider") != 403 {
		t.Fatal("staff selected transport billing type")
	}
	actor = owner
	if request("rider") != 200 || request("rider") != 200 || request("driver") != 409 {
		t.Fatal("selection must be owned, idempotent and immutable")
	}
	if checkTransportPlan(ctx, db, provider, riderPlan, 12) != nil || checkTransportPlan(ctx, db, provider, driverPlan, 1) == nil {
		t.Fatal("wrong transport purchase gate")
	}
	var unchanged bool
	if err = db.QueryRow(ctx, `SELECT status='active' AND current_period_end='2027-01-01T00:00:00Z'::timestamptz FROM provider_subscriptions WHERE provider_id=$1`, provider).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("existing paid access changed", err)
	}
}
