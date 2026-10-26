package controllers

import (
	"across/backend/internal/config"
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderCheckoutCancellationRecovery(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,email text DEFAULT 'seller@example.test',full_name text DEFAULT 'Seller',phone text DEFAULT '07060507214',email_verified boolean DEFAULT true,is_active boolean DEFAULT true);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,owner_user_id uuid,business_name text DEFAULT 'Seller',contact_email text DEFAULT 'seller@example.test',contact_phone text DEFAULT '07060507214',verification_status text DEFAULT 'approved',country_code text DEFAULT 'NG',is_active boolean DEFAULT true);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text DEFAULT 'owner',is_active boolean DEFAULT true);
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,name text DEFAULT 'Basic',code text DEFAULT 'provider-monthly',description text DEFAULT '',listing_limit int DEFAULT 5,features jsonb DEFAULT '{}',amount_ngn numeric DEFAULT 500,flutterwave_plan_id bigint DEFAULT 123,is_active boolean DEFAULT true);
 CREATE TABLE provider_subscriptions(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),provider_id uuid,plan_id uuid,status text,created_at timestamptz DEFAULT now(),tx_ref text,customer_email text,expected_amount_ngn numeric,billing_mode text DEFAULT 'recurring',current_period_end timestamptz,starts_at timestamptz,last_payment_at timestamptz,flutterwave_transaction_id text,updated_at timestamptz DEFAULT now(),version int DEFAULT 1);
 CREATE TABLE provider_subscription_payments(subscription_id uuid,flutterwave_transaction_id text UNIQUE,tx_ref text,amount numeric,currency_code text);
 CREATE TABLE provider_marketplace_events(provider_id uuid,event_type text,metadata jsonb);
 ALTER TABLE payments ADD COLUMN provider_subscription_id uuid,ADD COLUMN user_id uuid,ADD COLUMN country_code text,ADD COLUMN amount numeric,ADD COLUMN idempotency_key text,ADD COLUMN payment_method text,ADD COLUMN checkout_url text,ADD COLUMN provider_status text,ADD COLUMN failure_code text,ADD COLUMN failure_message text;`)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "065_prepaid_provider_subscription_periods.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	renewalSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "094_provider_renewal_controls.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(renewalSQL)); err != nil {
		t.Fatal(err)
	}

	user, seller, plan := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO users(id) VALUES($1)`, user)
	exec(`INSERT INTO provider_organizations(id,owner_user_id) VALUES($1,$2)`, seller, user)
	exec(`INSERT INTO provider_members(provider_id,user_id) VALUES($1,$2)`, seller, user)
	exec(`INSERT INTO provider_subscription_plans(id) VALUES($1)`, plan)
	ref := "PROVIDER-" + uuid.NewString()
	sub := uuid.NewString()
	exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,tx_ref,expected_amount_ngn,billing_mode) VALUES($1,$2,$3,'pending',$4,500,'one_time')`, sub, seller, plan, ref)
	exec(`INSERT INTO payments(provider,purpose,provider_subscription_id,user_id,provider_reference,checkout_url,payment_status) VALUES('flutterwave','provider_subscription',$1,$2,$3,'https://checkout.flutterwave.com/v3/hosted/pay/old','processing')`, sub, user, ref)
	gatewayTxn := "1001"
	status := "notfound"
	disableOK := true
	inactive := false
	requests := 0
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		code := 200
		body := ""
		if req.URL.Path == "/v3/payments/link/disable" {
			var payload map[string]string
			_ = json.NewDecoder(req.Body).Decode(&payload)
			if req.Method != "POST" || payload["link"] != "https://checkout.flutterwave.com/v3/hosted/pay/old" {
				t.Fatalf("unowned link: %v", payload)
			}
			body = `{"status":"success","data":true}`
			if !disableOK {
				code = 503
				body = `{"status":"error"}`
			}
			if inactive {
				code = 400
				body = `{"status":"error","message":"This payment link is currently inactive","data":null}`
			}
		} else if req.URL.Path == "/v3/payments" {
			body = `{"status":"success","data":{"link":"https://checkout.flutterwave.com/v3/hosted/pay/new"}}`
		} else {
			if req.URL.Query().Get("tx_ref") != ref {
				t.Fatal("wrong reference", req.URL)
			}
			switch status {
			case "notfound":
				code = 400
				body = `{"status":"error","message":"No transaction was found for this id","data":null}`
			case "outage":
				code = 503
				body = `{"status":"error","message":"No transaction was found for this id"}`
			case "unknown":
				code = 400
				body = `{"status":"error","message":"Unknown gateway problem"}`
			default:
				body = `{"status":"success","data":{"id":` + gatewayTxn + `,"tx_ref":"` + ref + `","status":"` + status + `","amount":500,"currency":"NGN"}}`
			}
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	gateway := newFlutterwaveProvider("test", client)
	payment := &PaymentController{db: db, provider: gateway}
	market := &ProviderMarketplaceController{db: db, paymentProvider: gateway, cfg: config.Config{ProviderSubscriptionsEnforced: true}}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", user); return c.Next() })
	app.Post("/cancel", payment.CancelUnpaidProviderCheckout)
	app.Post("/checkout", market.SubscriptionCheckout)
	app.Get("/renewal", market.ProviderRenewalStatus)
	call := func(path, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, e := app.Test(req, 5000)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	current := func() string {
		t.Helper()
		var value string
		if e := db.QueryRow(ctx, `SELECT status FROM provider_subscriptions WHERE id=$1`, sub).Scan(&value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	for _, s := range []string{"pending", "outage", "unknown"} {
		status = s
		if code, _ := call("/cancel", `{}`); code < 400 || current() != "pending" {
			t.Fatal("uncertain payment unlocked", s, code)
		}
	}
	disableOK = false
	status = "notfound"
	if code, _ := call("/cancel", `{}`); code != 503 || current() != "pending" {
		t.Fatal("failed disable unlocked", code)
	}
	disableOK = true
	before := requests
	call("/cancel", `{"tx_ref":"PROVIDER-foreign"}`)
	if requests != before || current() != "pending" {
		t.Fatal("foreign checkout touched")
	}
	exec(`UPDATE provider_members SET role='staff'`)
	if code, _ := call("/cancel", `{}`); code != 403 {
		t.Fatal("staff cancelled", code)
	}
	exec(`UPDATE provider_members SET role='owner'`)
	lock, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = lock.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('provider-renewal:'||$1,0))`, seller)
	if e != nil {
		t.Fatal(e)
	}
	if code, _ := call("/cancel", `{}`); code != 409 {
		t.Fatal("parallel action accepted", code)
	}
	_ = lock.Rollback(ctx)
	// Recover an already disabled link after an interrupted first request.
	inactive = true
	if code, out := call("/cancel", `{}`); code != 200 || out["payment_state"] != "cancelled" || current() != "cancelled" {
		t.Fatal("did not recover unpaid checkout", code, out)
	}
	// Cancellation is idempotent and preserves the old ledger/reference.
	if code, out := call("/cancel", `{}`); code != 200 || out["payment_state"] != "cancelled" {
		t.Fatal(code, out)
	}
	if code, out := call("/checkout", `{"plan_id":"`+plan+`","payment_method":"banktransfer"}`); code != 202 || out["billing_mode"] != "one_time" {
		t.Fatal("transfer retry blocked", code, out)
	}
	// A delayed, verified payment still settles once, rather than being lost.
	if e = settleProviderSubscription(ctx, db, ref, "1001", 500, "NGN"); e != nil {
		t.Fatal(e)
	}
	if e = settleProviderSubscription(ctx, db, ref, "1001", 500, "NGN"); e != nil {
		t.Fatal(e)
	}
	var count int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM provider_subscription_payments WHERE subscription_id=$1`, sub).Scan(&count)
	if count != 1 || current() != "active" {
		t.Fatal("lost/repeated delayed payment", count, current())
	}
	exec(`UPDATE provider_subscriptions SET status='cancelled' WHERE provider_id=$1 AND id<>$2`, seller, sub)
	// A payment that completes before cancellation is activated, never discarded.
	sub = uuid.NewString()
	ref = "PROVIDER-" + uuid.NewString()
	gatewayTxn = "1002"
	exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,tx_ref,expected_amount_ngn,billing_mode) VALUES($1,$2,$3,'pending',$4,500,'one_time')`, sub, seller, plan, ref)
	exec(`INSERT INTO payments(provider,purpose,provider_subscription_id,user_id,provider_reference,checkout_url,payment_status) VALUES('flutterwave','provider_subscription',$1,$2,$3,'https://checkout.flutterwave.com/v3/hosted/pay/old','processing')`, sub, user, ref)
	status = "successful"
	if code, out := call("/cancel", `{}`); code != 200 || out["payment_state"] != "settled" || current() != "active" {
		t.Fatal("paid checkout cancelled", code, out)
	}
}
