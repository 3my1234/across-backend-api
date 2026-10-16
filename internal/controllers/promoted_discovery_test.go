package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func paidDiscoveryTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db := settlementTestDB(t)
	ctx := context.Background()
	// Exercise the actual public endpoint, including its rank-aware cursor and
	// final batch enrichment. Lifecycle eligibility is covered with real migrations.
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,avatar_url text);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,owner_user_id uuid,logo_url text DEFAULT '',business_name text,verification_status text DEFAULT 'approved',is_active boolean DEFAULT true,paid boolean DEFAULT false);
 CREATE TABLE countries_config(country_code text,currency_code text,is_active boolean,active_payment_gateways text[]);
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,badge_tier text,listing_limit int);
 CREATE TABLE provider_subscriptions(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),provider_id uuid,plan_id uuid,status text,starts_at timestamptz,current_period_end timestamptz,created_at timestamptz DEFAULT now());
 CREATE TABLE provider_subscription_payments(subscription_id uuid,amount numeric,currency_code text);
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,listing_type text DEFAULT 'artisan',title text DEFAULT 'Service',slug text DEFAULT '',description text DEFAULT '',category text DEFAULT '',address_line text DEFAULT '',city text DEFAULT 'Abuja',state text DEFAULT 'FCT',country_code text DEFAULT 'NG',price numeric DEFAULT 100,currency_code text DEFAULT 'NGN',pricing_unit text DEFAULT 'job',capacity int DEFAULT 1,media_urls text[] DEFAULT '{}',attributes jsonb DEFAULT '{}',status text DEFAULT 'approved',moderation_notes text,published_at timestamptz,created_at timestamptz DEFAULT now(),latitude numeric DEFAULT 9,longitude numeric DEFAULT 7,service_radius_km numeric,is_mobile_service boolean DEFAULT false,is_available_now boolean DEFAULT true,review_count int DEFAULT 0,review_rating_sum int DEFAULT 0);
 INSERT INTO countries_config VALUES('NG','NGN',true,ARRAY['flutterwave']);
 INSERT INTO provider_subscription_plans VALUES(gen_random_uuid(),'bronze',5);`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/079_permanent_free_provider_tier.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, strings.Split(string(raw), "CREATE OR REPLACE FUNCTION")[0]); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPaidDiscoveryEligibilityMatchesPublicIdentity(t *testing.T) {
	db := paidDiscoveryTestDB(t)
	ctx := context.Background()
	paid, free, sub := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO provider_organizations(id,business_name) VALUES($1,'Older subscription'),($2,'Newer Free')`, []any{paid, free}},
		{`INSERT INTO provider_listings(id,provider_id,created_at) VALUES(gen_random_uuid(),$1,'2020-01-01'),(gen_random_uuid(),$2,'2026-10-01')`, []any{paid, free}},
		{`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,starts_at,current_period_end) SELECT $1,$2,id,'active',now()-interval '1 day',now()+interval '1 month' FROM provider_subscription_plans`, []any{sub, paid}},
	} {
		if _, err := db.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	app := fiber.New()
	m := &ProviderMarketplaceController{db: db}
	app.Get("/listings", m.ListPublicListings)
	for _, tc := range []struct {
		name, sql string
		wantPaid  bool
	}{
		{"unpaid", `SELECT 1`, false},
		{"zero payment", `INSERT INTO provider_subscription_payments SELECT id,0,'NGN' FROM provider_subscriptions`, false},
		{"wrong currency", `INSERT INTO provider_subscription_payments SELECT id,500,'USD' FROM provider_subscriptions`, false},
		{"paid", `INSERT INTO provider_subscription_payments SELECT id,500,'NGN' FROM provider_subscriptions`, true},
		{"future start", `UPDATE provider_subscriptions SET starts_at=now()+interval '1 day'`, false},
		{"null start", `UPDATE provider_subscriptions SET starts_at=NULL`, true},
		{"expired", `UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second'`, false},
		{"cancelled", `UPDATE provider_subscriptions SET current_period_end=now()+interval '1 month',status='cancelled'`, false},
		{"restored", `UPDATE provider_subscriptions SET status='active'`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(ctx, tc.sql); err != nil {
				t.Fatal(err)
			}
			resp, err := app.Test(httptest.NewRequest("GET", "/listings?limit=20", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var body struct {
				Items []struct {
					ProviderID string `json:"provider_id"`
					Identity   struct {
						Paid bool `json:"paid_active"`
					} `json:"provider_identity"`
				} `json:"items"`
			}
			if err = json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != 200 || len(body.Items) != 2 {
				t.Fatalf("status=%d body=%+v error=%v", resp.StatusCode, body, err)
			}
			wantFirst := free
			if tc.wantPaid {
				wantFirst = paid
			}
			if body.Items[0].ProviderID != wantFirst {
				t.Fatalf("paid promotion order: got %s want %s", body.Items[0].ProviderID, wantFirst)
			}
			for _, item := range body.Items {
				if item.ProviderID == paid && item.Identity.Paid != tc.wantPaid {
					t.Fatal("feed priority disagrees with public identity")
				}
			}
		})
	}
	// Eligibility must not make suspended businesses visible; market closure is immediate.
	if _, err := db.Exec(ctx, `UPDATE provider_organizations SET verification_status='suspended' WHERE id=$1`, paid); err != nil {
		t.Fatal(err)
	}
	resp, err := app.Test(httptest.NewRequest("GET", "/listings", nil))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Items []struct {
			ProviderID string `json:"provider_id"`
		} `json:"items"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil || len(body.Items) != 1 || body.Items[0].ProviderID != free {
		t.Fatalf("suspended listing exposed: %+v %v", body, err)
	}
	resp.Body.Close()
	if _, err := db.Exec(ctx, `UPDATE countries_config SET is_active=false`); err != nil {
		t.Fatal(err)
	}
	resp, err = app.Test(httptest.NewRequest("GET", "/listings", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil || len(body.Items) != 0 {
		t.Fatalf("inactive market exposed: %+v %v", body, err)
	}
}

func TestPaidDiscoveryCursorDoesNotSkipNewerFreeListings(t *testing.T) {
	db := paidDiscoveryTestDB(t)
	ctx := context.Background()
	paid, free := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO provider_organizations(id,business_name,paid) VALUES($1,'Paid',true),($2,'Free',false)`, paid, free); err != nil {
		t.Fatal(err)
	}
	// Multiple subscriptions and payments must not duplicate a provider's cards.
	if _, err := db.Exec(ctx, `INSERT INTO provider_subscriptions(provider_id,plan_id,status,starts_at,current_period_end)
 SELECT $1,id,'active',now()-interval '1 day',now()+interval '1 month' FROM provider_subscription_plans CROSS JOIN generate_series(1,2);
 INSERT INTO provider_subscription_payments SELECT id,500,'NGN' FROM provider_subscriptions CROSS JOIN generate_series(1,2);`, pgx.QueryExecModeSimpleProtocol, paid); err != nil {
		t.Fatal(err)
	}
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for i, id := range ids {
		owner, date := paid, "2020-01-01"
		if i >= 2 {
			owner, date = free, "2026-10-01"
		}
		if _, err := db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,created_at) VALUES($1,$2,$3::timestamptz)`, id, owner, date); err != nil {
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
	if _, err := db.Exec(ctx, `UPDATE provider_listings SET category=CASE WHEN id=ANY($1::uuid[]) THEN 'Independent driver' ELSE 'Hotel events' END,description='Hotels and plumbing also mentioned'`, ids[:2]); err != nil {
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
