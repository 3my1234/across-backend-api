package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestPaidDiscoveryCursorDoesNotSkipNewerFreeListings(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	// Exercise the actual public endpoint, including its rank-aware cursor and
	// final batch enrichment. Lifecycle eligibility is covered with real migrations.
	_, err := db.Exec(ctx, `CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text,verification_status text DEFAULT 'approved',is_active boolean DEFAULT true,paid boolean DEFAULT false);
 CREATE TABLE countries_config(country_code text,currency_code text,is_active boolean,active_payment_gateways text[]);
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,listing_type text DEFAULT 'artisan',title text DEFAULT 'Service',slug text DEFAULT '',description text DEFAULT '',category text DEFAULT '',address_line text DEFAULT '',city text DEFAULT 'Abuja',state text DEFAULT 'FCT',country_code text DEFAULT 'NG',price numeric DEFAULT 100,currency_code text DEFAULT 'NGN',pricing_unit text DEFAULT 'job',capacity int DEFAULT 1,media_urls text[] DEFAULT '{}',attributes jsonb DEFAULT '{}',status text DEFAULT 'approved',moderation_notes text,published_at timestamptz,created_at timestamptz DEFAULT now(),latitude numeric DEFAULT 9,longitude numeric DEFAULT 7,service_radius_km numeric,is_mobile_service boolean DEFAULT false,is_available_now boolean DEFAULT true,review_count int DEFAULT 0,review_rating_sum int DEFAULT 0);
 INSERT INTO countries_config VALUES('NG','NGN',true,ARRAY['flutterwave']);
 CREATE VIEW provider_public_identity AS SELECT id AS provider_id,business_name AS name,''::text AS avatar_url,true AS verified,paid AS paid_active,CASE WHEN paid THEN 'bronze' ELSE '' END AS badge_tier,CASE WHEN paid THEN 5 ELSE 1 END AS listing_limit FROM provider_organizations;`)
	if err != nil {
		t.Fatal(err)
	}
	paid, free := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,business_name,paid) VALUES($1,'Paid',true),($2,'Free',false)`, paid, free); err != nil {
		t.Fatal(err)
	}
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for i, id := range ids {
		owner, date := paid, "2020-01-01"
		if i >= 2 {
			owner, date = free, "2026-10-01"
		}
		if _, err = db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,created_at) VALUES($1,$2,$3::timestamptz)`, id, owner, date); err != nil {
			t.Fatal(err)
		}
	}
	app := fiber.New()
	controller := &ProviderMarketplaceController{db: db}
	app.Get("/listings", controller.ListPublicListings)
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 4; page++ {
		response, err := app.Test(httptest.NewRequest("GET", "/listings?limit=1&cursor="+url.QueryEscape(cursor), nil))
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Items []struct {
				ID         string `json:"id"`
				ProviderID string `json:"provider_id"`
			}
			Cursor string `json:"next_cursor"`
		}
		if err = json.NewDecoder(response.Body).Decode(&body); err != nil || response.StatusCode != 200 || len(body.Items) != 1 {
			t.Fatalf("page %d status %d: %v", page, response.StatusCode, err)
		}
		item := body.Items[0]
		if seen[item.ID] {
			t.Fatal("duplicate across paid/free cursor boundary")
		}
		seen[item.ID] = true
		expected := paid
		if page >= 2 {
			expected = free
		}
		if item.ProviderID != expected {
			t.Fatalf("page %d provider=%s wanted %s", page, item.ProviderID, expected)
		}
		cursor = body.Cursor
		if page < 3 && cursor == "" {
			t.Fatal("lost free listings after paid page")
		}
	}
	if len(seen) != 4 || cursor != "" {
		t.Fatal("pagination did not terminate after all listings")
	}
	// Related services must filter the actual category, not match unrelated
	// descriptions or merely share the generic artisan type.
	if _, err = db.Exec(ctx, `UPDATE provider_listings SET category=CASE WHEN id=ANY($1::uuid[]) THEN 'Independent driver' ELSE 'Hotel events' END,description='Hotels and plumbing also mentioned'`, ids[:2]); err != nil {
		t.Fatal(err)
	}
	response, err := app.Test(httptest.NewRequest("GET", "/listings?category="+url.QueryEscape(" independent DRIVER ")+"&exclude="+ids[0]+"&type=artisan", nil))
	if err != nil {
		t.Fatal(err)
	}
	var related struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err = json.NewDecoder(response.Body).Decode(&related); err != nil || response.StatusCode != 200 || len(related.Items) != 1 || related.Items[0].ID != ids[1] {
		t.Fatalf("related service filtering: status %d body %+v err %v", response.StatusCode, related, err)
	}
	bad, err := app.Test(httptest.NewRequest("GET", "/listings?exclude=not-a-uuid", nil))
	if err != nil || bad.StatusCode != 400 {
		t.Fatal("malformed related service exclusion was accepted", err)
	}
}
