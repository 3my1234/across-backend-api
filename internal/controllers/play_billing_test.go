package controllers

import (
	"across/backend/internal/playbilling"
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlayBillingRequiresExistingAuthenticatedBusinessOwner(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	owner, staff, other, provider := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,is_active bool,email_verified bool);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,is_active bool);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active bool);
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,name text,amount_ngn numeric,is_active bool);
 CREATE TABLE play_provider_products(product_id text,base_plan_id text,plan_id uuid,duration_months int,amount_minor bigint,enabled bool);
 CREATE TABLE provider_subscriptions(id uuid PRIMARY KEY,status text,current_period_end timestamptz);
 CREATE TABLE play_provider_purchases(provider_id uuid,subscription_id uuid,state text,auto_renews bool,needs_acknowledgement bool,checked_at timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO users VALUES($1::uuid,true,true),($2::uuid,true,true),($3::uuid,true,true)`, owner, staff, other)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO provider_organizations VALUES($1::uuid,true)`, provider)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO provider_members VALUES($1::uuid,$2::uuid,'owner',true),($1::uuid,$3::uuid,'staff',true)`, provider, owner, staff)
	if err != nil {
		t.Fatal(err)
	}
	controller := &PlayBillingController{db: db, store: &playbilling.Store{DB: db}}
	for _, test := range []struct {
		user string
		want int
	}{{"", 401}, {staff, 403}, {other, 403}, {owner, 200}} {
		app := fiber.New()
		app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", test.user); return c.Next() })
		app.Get("/billing", controller.Catalog)
		response, e := app.Test(httptest.NewRequest("GET", "/billing", nil))
		if e != nil {
			t.Fatal(e)
		}
		if response.StatusCode != test.want {
			t.Fatalf("Owner authorization: %d expected %d", response.StatusCode, test.want)
		}
		if test.want == 200 {
			var body map[string]any
			if json.NewDecoder(response.Body).Decode(&body) != nil || body["account_id"] != playbilling.AccountID(provider) {
				t.Fatal("Response did not bind existing business identity")
			}
			if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
				t.Fatal("Billing identity was cacheable")
			}
		}
		response.Body.Close()
	}
}
