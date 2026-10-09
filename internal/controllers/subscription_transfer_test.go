package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestSubscriptionTransferCheckoutAndVerifiedEntitlement(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,email text DEFAULT 'seller@example.test',full_name text DEFAULT 'Seller',phone text DEFAULT '07060507214',email_verified boolean DEFAULT true,is_active boolean DEFAULT true);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,owner_user_id uuid,business_name text DEFAULT 'Seller',contact_email text DEFAULT 'seller@example.test',contact_phone text DEFAULT '07060507214',verification_status text DEFAULT 'approved',country_code text DEFAULT 'NG',is_active boolean DEFAULT true);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text DEFAULT 'owner',is_active boolean DEFAULT true);
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,name text DEFAULT 'Basic',amount_ngn numeric DEFAULT 500,flutterwave_plan_id bigint DEFAULT 123,is_active boolean DEFAULT true);
 CREATE TABLE provider_subscriptions(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),provider_id uuid,plan_id uuid,status text,tx_ref text,customer_email text,expected_amount_ngn numeric,billing_mode text DEFAULT 'recurring',current_period_end timestamptz,starts_at timestamptz,last_payment_at timestamptz,flutterwave_transaction_id text,updated_at timestamptz DEFAULT now(),version int DEFAULT 1);
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
	user, seller, plan := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO users(id) VALUES($1);`, user); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,owner_user_id) VALUES($1,$2)`, seller, user); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_members(provider_id,user_id) VALUES($1,$2)`, seller, user); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_subscription_plans(id) VALUES($1)`, plan); err != nil {
		t.Fatal(err)
	}
	var gatewayBodies []map[string]any
	planChecks := 0
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			planChecks++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"id":123,"amount":500,"currency":"NGN","interval":"monthly","status":"active"}}`))}, nil
		}
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		gatewayBodies = append(gatewayBodies, payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"link":"https://checkout.flutterwave.com/test"}}`))}, nil
	})}
	controller := &ProviderMarketplaceController{db: db, cfg: config.Config{ProviderSubscriptionsEnforced: true}, paymentProvider: newFlutterwaveProvider("test", client)}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", user); return c.Next() })
	app.Post("/checkout", controller.SubscriptionCheckout)
	checkout := func(method string, periods ...int) (int, map[string]any) {
		t.Helper()
		months := 1
		if len(periods) > 0 {
			months = periods[0]
		}
		payload, _ := json.Marshal(map[string]any{"plan_id": plan, "payment_method": method, "duration_months": months})
		req := httptest.NewRequest("POST", "/checkout", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(res.Body).Decode(&body)
		return res.StatusCode, body
	}
	status, card := checkout("")
	if status != 202 {
		t.Fatal(status, card)
	}
	if planChecks != 1 || gatewayBodies[0]["payment_options"] != "card" || gatewayBodies[0]["payment_plan"] != float64(123) {
		t.Fatal(gatewayBodies, planChecks)
	}
	for _, bad := range []int{-1, 2, 24} {
		status, body := checkout("banktransfer", bad)
		if status != 422 {
			t.Fatal("invalid duration accepted", status, body)
		}
	}
	status, body := checkout("card", 3)
	if status != 422 {
		t.Fatal("nonmonthly recurring charge accepted", status, body)
	}
	for _, months := range []int{3, 6, 12} {
		status, paid := checkout("banktransfer", months)
		if status != 202 {
			t.Fatal(status, paid)
		}
		amount := float64(500 * months)
		ref := paid["tx_ref"].(string)
		if paid["amount_ngn"] != amount || gatewayBodies[len(gatewayBodies)-1]["amount"] != amount {
			t.Fatal("incorrect prepaid total", paid)
		}
		if err = settleProviderSubscription(ctx, db, ref, ref, amount-1, "NGN"); err == nil {
			t.Fatal("prepaid underpayment accepted")
		}
		// Price changes after checkout must not alter this purchase's saved amount.
		if _, err = db.Exec(ctx, "UPDATE provider_subscription_plans SET amount_ngn=700 WHERE id=$1", plan); err != nil {
			t.Fatal(err)
		}
		if err = settleProviderSubscription(ctx, db, ref, ref, amount, "NGN"); err != nil {
			t.Fatal(err)
		}
		var start, end time.Time
		if err = db.QueryRow(ctx, "SELECT starts_at,current_period_end FROM provider_subscriptions WHERE tx_ref=$1", ref).Scan(&start, &end); err != nil {
			t.Fatal(err)
		}
		var days float64
		_ = db.QueryRow(ctx, "SELECT EXTRACT(EPOCH FROM current_period_end-starts_at)/86400 FROM provider_subscriptions WHERE tx_ref=$1", ref).Scan(&days)
		if days < float64(months*28) || days > float64(months*31+1) {
			t.Fatal("wrong paid period", months, start, end, days)
		}
		if err = settleProviderSubscription(ctx, db, ref, ref, amount, "NGN"); err != nil {
			t.Fatal(err)
		}
		var again time.Time
		_ = db.QueryRow(ctx, "SELECT current_period_end FROM provider_subscriptions WHERE tx_ref=$1", ref).Scan(&again)
		if !again.Equal(end) {
			t.Fatal("callback extended prepaid access")
		}
		if _, err = db.Exec(ctx, "UPDATE provider_subscriptions SET duration_months=1 WHERE tx_ref=$1", ref); err == nil {
			t.Fatal("purchase duration changed")
		}
		if _, err = db.Exec(ctx, "UPDATE provider_subscriptions SET status='expired',current_period_end=now()-interval '1 day' WHERE tx_ref=$1", ref); err != nil {
			t.Fatal(err)
		}
		_, _ = db.Exec(ctx, "UPDATE provider_subscription_plans SET amount_ngn=500 WHERE id=$1", plan)
	}
	status, transfer := checkout("banktransfer")
	if status != 202 || transfer["billing_mode"] != "one_time" {
		t.Fatal(status, transfer)
	}
	if planChecks != 1 || gatewayBodies[len(gatewayBodies)-1]["payment_options"] != "banktransfer" || gatewayBodies[len(gatewayBodies)-1]["payment_plan"] != nil {
		t.Fatal("transfer must not create recurring card checkout", gatewayBodies[1])
	}
	ref := transfer["tx_ref"].(string)
	var mode, method string
	if err = db.QueryRow(ctx, `SELECT s.billing_mode,p.payment_method FROM provider_subscriptions s JOIN payments p ON p.provider_subscription_id=s.id WHERE s.tx_ref=$1`, ref).Scan(&mode, &method); err != nil || mode != "one_time" || method != "banktransfer" {
		t.Fatal(mode, method, err)
	}
	if err = settleProviderSubscription(ctx, db, ref, "tx-transfer", 499, "NGN"); err == nil {
		t.Fatal("underpayment accepted")
	}
	if err = settleProviderSubscription(ctx, db, ref, "tx-transfer", 500, "USD"); err == nil {
		t.Fatal("wrong currency accepted")
	}
	if err = settleProviderSubscription(ctx, db, ref, "tx-transfer", 500, "NGN"); err != nil {
		t.Fatal(err)
	}
	var end time.Time
	if err = db.QueryRow(ctx, `SELECT current_period_end FROM provider_subscriptions WHERE tx_ref=$1`, ref).Scan(&end); err != nil || time.Until(end) < 27*24*time.Hour || time.Until(end) > 32*24*time.Hour {
		t.Fatal(end, err)
	}
	if err = settleProviderSubscription(ctx, db, ref, "tx-transfer", 500, "NGN"); err != nil {
		t.Fatal(err)
	}
	var again time.Time
	_ = db.QueryRow(ctx, `SELECT current_period_end FROM provider_subscriptions WHERE tx_ref=$1`, ref).Scan(&again)
	if !again.Equal(end) {
		t.Fatal("duplicate granted another month")
	}
	if err = settleProviderSubscription(ctx, db, ref, "different-payment", 500, "NGN"); err == nil {
		t.Fatal("one-time reference reused for another payment")
	}
	if _, err = db.Exec(ctx, `UPDATE provider_subscriptions SET current_period_end=now()-interval '1 day',status='expired' WHERE tx_ref=$1`, ref); err != nil {
		t.Fatal(err)
	}
	if err = settleProviderSubscription(ctx, db, ref, "tx-transfer", 500, "NGN"); err != nil {
		t.Fatal(err)
	}
	var subscriptionStatus string
	_ = db.QueryRow(ctx, `SELECT status FROM provider_subscriptions WHERE tx_ref=$1`, ref).Scan(&subscriptionStatus)
	if subscriptionStatus != "expired" {
		t.Fatal("old callback reactivated an expired transfer month")
	}
}
