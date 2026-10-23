package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestNativeRequestExactLookupPreservesOwnership(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text);
 CREATE TABLE users(id uuid PRIMARY KEY,full_name text,email text,phone text);
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,title text,listing_type text);
 CREATE TABLE provider_requests(id uuid PRIMARY KEY,provider_id uuid,user_id uuid,listing_id uuid,request_type text DEFAULT 'enquiry',status text DEFAULT 'pending',starts_at timestamptz,ends_at timestamptz,party_size int DEFAULT 1,message text DEFAULT '',search_text text DEFAULT '',created_at timestamptz DEFAULT now());
 CREATE TABLE provider_listing_reviews(request_id uuid,rating int,review_text text);`)
	if err != nil {
		t.Fatal(err)
	}
	owner, other, buyer, listing, wanted, unrelated, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO provider_organizations VALUES($1,'One'),($2,'Other')`, owner, other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO users VALUES($1,'Synthetic buyer','buyer@example.test','')`, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_listings VALUES($1,'Synthetic service','artisan')`, listing); err != nil {
		t.Fatal(err)
	}
	for id, provider := range map[string]string{wanted: owner, unrelated: owner, foreign: other} {
		if _, err = db.Exec(ctx, `INSERT INTO provider_requests(id,provider_id,user_id,listing_id) VALUES($1,$2,$3,$4)`, id, provider, buyer, listing); err != nil {
			t.Fatal(err)
		}
	}
	app := fiber.New()
	controller := &ProviderMarketplaceController{db: db}
	// Exercise the shared query with a fixed authenticated provider scope. Production
	// handlers obtain this ID from providerForUser, never from request parameters.
	app.Get("/", func(c *fiber.Ctx) error { return controller.listRequests(c, "provider", owner) })
	for _, sample := range []struct {
		id            string
		status, count int
	}{{wanted, 200, 1}, {foreign, 200, 0}, {uuid.NewString(), 200, 0}, {"not-a-uuid", 400, 0}, {"", 200, 2}} {
		response, e := app.Test(httptest.NewRequest("GET", "/?request_id="+sample.id, nil))
		if e != nil {
			t.Fatal(e)
		}
		if response.StatusCode != sample.status {
			t.Fatalf("expected %d got %d", sample.status, response.StatusCode)
		}
		if sample.status == 200 {
			var body struct {
				Items []map[string]any `json:"items"`
			}
			if e = json.NewDecoder(response.Body).Decode(&body); e != nil {
				t.Fatal(e)
			}
			if len(body.Items) != sample.count {
				t.Fatalf("expected %d rows got %d", sample.count, len(body.Items))
			}
			if sample.id == wanted && body.Items[0]["id"] != wanted {
				t.Fatal("exact notification opened an unrelated request")
			}
		}
		response.Body.Close()
	}
}
