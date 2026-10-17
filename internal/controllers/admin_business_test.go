package controllers

import (
	"across/backend/internal/config"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestAdminBusinessSubscribers(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(is_active boolean);INSERT INTO users VALUES(true),(true),(true),(true),(false);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text,contact_email text,is_active boolean,verification_status text);
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,name text);
 INSERT INTO provider_subscription_plans VALUES('11111111-1111-4111-8111-111111111111','Bronze');
 CREATE TABLE provider_subscriptions(id uuid PRIMARY KEY,provider_id uuid,plan_id uuid DEFAULT '11111111-1111-4111-8111-111111111111',status text DEFAULT 'active',starts_at timestamptz,current_period_end timestamptz,created_at timestamptz DEFAULT now(),billing_mode text DEFAULT 'prepaid',duration_months int DEFAULT 1,last_payment_at timestamptz);
 CREATE TABLE provider_subscription_payments(subscription_id uuid,currency_code text,amount numeric);
 CREATE TABLE provider_listings(status text);INSERT INTO provider_listings VALUES('pending'),('approved');
 CREATE TABLE products(provider_id uuid,moderation_status text);INSERT INTO products VALUES('11111111-1111-4111-8111-111111111111','pending'),(NULL,'pending');
 CREATE TABLE support_tickets(status text);INSERT INTO support_tickets VALUES('open'),('responded'),('closed');
 CREATE TABLE chat_reports(state text);INSERT INTO chat_reports VALUES('open'),('resolved');
 CREATE TABLE complaints(status text);INSERT INTO complaints VALUES('unresolved'),('unresolved'),('resolved');`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("22222222-2222-4222-8222-%012d", i)
		sub := fmt.Sprintf("33333333-3333-4333-8333-%012d", i)
		verified := "approved"
		if i == 3 {
			verified = "pending"
		}
		_, err = db.Exec(ctx, `INSERT INTO provider_organizations VALUES($1,$2,$3,$4,$5);`, id, fmt.Sprintf("Business %d", i), fmt.Sprintf("provider%d@example.test", i), i != 7, verified)
		if err != nil {
			t.Fatal(err)
		}
		end := time.Now().Add(14 * 24 * time.Hour)
		if i == 2 {
			end = time.Now().Add(3 * 24 * time.Hour)
		}
		if i == 4 {
			end = time.Now().Add(-24 * time.Hour)
		}
		status := "active"
		if i == 10 {
			status = "past_due"
		}
		_, err = db.Exec(ctx, `INSERT INTO provider_subscriptions(id,provider_id,status,current_period_end) VALUES($1,$2,$3,$4)`, sub, id, status, end)
		if err != nil {
			t.Fatal(err)
		}
		if i == 5 {
			_, err = db.Exec(ctx, `UPDATE provider_subscriptions SET starts_at=now()+interval '1 day' WHERE id=$1`, sub)
			if err != nil {
				t.Fatal(err)
			}
		}
		currency := "NGN"
		if i == 8 {
			currency = "USD"
		}
		amount := 500
		if i == 9 {
			amount = 0
		}
		if i != 3 && i != 6 {
			_, err = db.Exec(ctx, `INSERT INTO provider_subscription_payments VALUES($1,$2,$3)`, sub, currency, amount)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// Overlapping confirmed periods for one provider must not inflate subscribers.
	_, err = db.Exec(ctx, `INSERT INTO provider_subscriptions(id,provider_id,current_period_end) VALUES('44444444-4444-4444-8444-444444444444','22222222-2222-4222-8222-000000000001',now()+interval '1 day');INSERT INTO provider_subscription_payments VALUES('44444444-4444-4444-8444-444444444444','NGN',500)`)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	admin := NewAdminController(db, config.Config{})
	app.Get("/overview", admin.BusinessOverview)
	app.Get("/subscribers", admin.Subscribers)
	get := func(path string) map[string]any {
		t.Helper()
		res, e := app.Test(httptest.NewRequest("GET", path, nil))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s status %d", path, res.StatusCode)
		}
		var data map[string]any
		if e = json.NewDecoder(res.Body).Decode(&data); e != nil {
			t.Fatal(e)
		}
		return data
	}
	overview := get("/overview")
	for key, want := range map[string]float64{"active_accounts": 4, "active_providers": 9, "verified_providers": 8, "paid_providers": 2, "expiring_providers": 1, "pending_providers": 1, "pending_services": 1, "pending_products": 1, "open_support": 1, "open_reports": 1, "unresolved_complaints": 2} {
		if overview[key] != want {
			t.Fatalf("%s=%v want %v", key, overview[key], want)
		}
	}
	first := get("/subscribers?limit=1")
	items := first["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["business_name"] != "Business 2" || first["has_more"] != true {
		t.Fatalf("first=%v", first)
	}
	second := get("/subscribers?limit=1&cursor=" + url.QueryEscape(first["next_cursor"].(string)))
	if second["has_more"] != false || second["items"].([]any)[0].(map[string]any)["business_name"] != "Business 1" {
		t.Fatalf("second=%v", second)
	}
	if len(get("/subscribers?status=expiring")["items"].([]any)) != 1 {
		t.Fatal("expiring filter incorrect")
	}
	if len(get("/subscribers?search=provider1%40example.test")["items"].([]any)) != 1 {
		t.Fatal("search incorrect")
	}
	if len(get("/subscribers?search=absent")["items"].([]any)) != 0 {
		t.Fatal("empty search incorrect")
	}
	for _, path := range []string{"/subscribers?cursor=invalid", "/subscribers?limit=101", "/subscribers?status=unknown", "/subscribers?cursor=" + encodeMarketplaceCursor(time.Now(), "bad-id")} {
		res, e := app.Test(httptest.NewRequest("GET", path, nil))
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatalf("invalid input %s returned %d", path, res.StatusCode)
		}
	}
}
