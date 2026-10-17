package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestSubscriptionRenewalCancellationAndNextPlan(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,is_active boolean DEFAULT true);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean DEFAULT true);
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,name text,amount_ngn numeric,is_active boolean DEFAULT true,flutterwave_plan_id bigint);
 CREATE TABLE provider_subscriptions(id uuid PRIMARY KEY,provider_id uuid,plan_id uuid,status text,billing_mode text,customer_email text,starts_at timestamptz,current_period_end timestamptz,created_at timestamptz DEFAULT now());
 CREATE TABLE provider_subscription_payments(subscription_id uuid,flutterwave_transaction_id text,paid_at timestamptz DEFAULT now());`)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "094_provider_renewal_controls.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	owner, provider, basic, growth, subscription := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, query, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO users VALUES($1)`, owner)
	exec(`INSERT INTO provider_organizations(id) VALUES($1)`, provider)
	exec(`INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner')`, provider, owner)
	exec(`INSERT INTO provider_subscription_plans VALUES($1,'Basic',500,true,123),($2,'Growth',1000,true,456)`, basic, growth)
	exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,billing_mode,customer_email,starts_at,current_period_end) VALUES($1,$2,$3,'active','recurring','owner@example.test',now()-interval '1 day',now()+interval '29 days')`, subscription, provider, basic)
	exec(`INSERT INTO provider_subscription_payments(subscription_id,flutterwave_transaction_id) VALUES($1,'1001')`, subscription)
	exec(`UPDATE provider_subscriptions SET gateway_plan_id=123 WHERE false`) // immutable snapshot is tested below on a new row.
	cancelled := map[string]bool{}
	puts := 0
	failVerify := false
	wrongPlan := false
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"status":"success","data":[]}`
		if req.Method == http.MethodGet {
			if req.URL.Query().Get("transaction_id") != "1001" || req.URL.Query().Get("email") != "owner@example.test" {
				t.Fatalf("not scoped to owned verified transaction: %s", req.URL)
			}
			items := []string{}
			for _, id := range []string{"41", "42"} {
				if !cancelled[id] || failVerify {
					plan := 123
					if wrongPlan {
						plan = 999
					}
					items = append(items, fmt.Sprintf(`{"id":%s,"plan":%d,"status":"active","customer":{"customer_email":"owner@example.test"}}`, id, plan))
				}
			}
			body = `{"status":"success","meta":{"page_info":{"total_pages":1}},"data":[` + strings.Join(items, ",") + `]}`
		} else if req.Method == http.MethodPut {
			puts++
			parts := strings.Split(req.URL.Path, "/")
			cancelled[parts[len(parts)-2]] = true
		} else {
			t.Fatalf("unexpected gateway request %s", req.Method)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	controller := &ProviderMarketplaceController{db: db, paymentProvider: newFlutterwaveProvider("test", client)}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", owner); return c.Next() })
	app.Get("/renewal", controller.ProviderRenewalStatus)
	app.Post("/renewal", controller.ProviderManageRenewal)
	app.Post("/checkout", controller.SubscriptionCheckout)
	call := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, e := app.Test(req, 5000)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var data map[string]any
		raw, _ := io.ReadAll(res.Body)
		if e = json.Unmarshal(raw, &data); e != nil {
			data = map[string]any{"error": string(raw)}
		}
		return res.StatusCode, data
	}
	var originalEnd time.Time
	if err = db.QueryRow(ctx, `SELECT current_period_end FROM provider_subscriptions WHERE id=$1`, subscription).Scan(&originalEnd); err != nil {
		t.Fatal(err)
	}
	status, data := call("POST", "/renewal", `{"next_plan_id":"`+growth+`"}`)
	if status != 200 || data["renewal_status"] != "stopped" || puts != 2 {
		t.Fatalf("schedule: %d %+v puts%d", status, data, puts)
	}
	status, data = call("GET", "/renewal", "")
	if status != 200 || data["next_plan_id"] != growth || data["current_plan_id"] != basic {
		t.Fatalf("status %d %+v", status, data)
	}
	var stillPlan, stillStatus string
	var stillEnd time.Time
	if err = db.QueryRow(ctx, `SELECT plan_id::text,status,current_period_end FROM provider_subscriptions WHERE id=$1`, subscription).Scan(&stillPlan, &stillStatus, &stillEnd); err != nil {
		t.Fatal(err)
	}
	if stillPlan != basic || stillStatus != "active" || !stillEnd.Equal(originalEnd) {
		t.Fatal("cancellation changed paid entitlement")
	}
	status, _ = call("POST", "/checkout", `{"plan_id":"`+growth+`"}`)
	if status != 409 {
		t.Fatal("active paid period allowed another checkout")
	}
	// Separate API replicas share the same renewal/checkout lock.
	lockTx, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "provider-renewal:"+provider); e != nil {
		t.Fatal(e)
	}
	status, _ = call("POST", "/renewal", `{}`)
	if status != 409 {
		t.Fatal("concurrent renewal change was not blocked")
	}
	status, _ = call("POST", "/checkout", `{}`)
	if status != 409 {
		t.Fatal("concurrent replacement checkout was not blocked")
	}
	if e = lockTx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	status, data = call("POST", "/renewal", `{}`)
	if status != 200 || data["next_plan_id"] != nil || puts != 2 {
		t.Fatalf("idempotent cancellation %d %+v", status, data)
	}
	exec(`UPDATE provider_members SET role='staff' WHERE provider_id=$1`, provider)
	status, _ = call("POST", "/renewal", `{}`)
	if status != 403 {
		t.Fatal("non-owner could cancel billing")
	}
	exec(`UPDATE provider_members SET role='owner' WHERE provider_id=$1`, provider)
	cancelled = map[string]bool{}
	wrongPlan = true
	status, _ = call("POST", "/renewal", `{}`)
	if status != 502 || puts != 2 {
		t.Fatal("unowned gateway plan was cancelled")
	}
	wrongPlan = false
	failVerify = true
	status, _ = call("POST", "/renewal", `{}`)
	if status != 502 {
		t.Fatal("unconfirmed cancellation reported success")
	}
	_, data = call("GET", "/renewal", "")
	if data["renewal_status"] != "uncertain" {
		t.Fatalf("uncertainty lost %+v", data)
	}
	exec(`UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE id=$1`, subscription)
	status, _ = call("POST", "/checkout", `{"plan_id":"`+growth+`"}`)
	if status != 409 {
		t.Fatal("uncertain old renewal allowed replacement billing")
	}
	failVerify = false
	status, _ = call("POST", "/renewal", `{}`)
	if status != 200 {
		t.Fatal("could not reconcile cancellation after retry")
	}
	status, _ = call("POST", "/renewal", `{"next_plan_id":"`+growth+`"}`)
	if status != 409 {
		t.Fatal("scheduled an expired entitlement")
	}
	// A new verified purchase clears the old preference from the status response.
	exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,billing_mode,customer_email,starts_at,current_period_end,created_at,gateway_plan_id) VALUES($1,$2,$3,'active','recurring','owner@example.test',now(),now()+interval '1 month',now()+interval '1 second',456)`, uuid.NewString(), provider, growth)
	_, data = call("GET", "/renewal", "")
	if data["renewal_status"] != "" || data["current_plan_id"] != growth {
		t.Fatalf("old preferences leaked into new subscription %+v", data)
	}
	if _, err = db.Exec(ctx, `UPDATE provider_subscriptions SET gateway_plan_id=999 WHERE gateway_plan_id=456`); err == nil {
		t.Fatal("gateway ownership snapshot was mutable")
	}
}
