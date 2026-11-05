package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestServiceDiscoveryRepeatedFiltersAndCursorAvoidGenericPlan(t *testing.T) {
	original := paidDiscoveryTestDB(t)
	cfg := original.Config().Copy()
	cfg.MaxConns = 1 // Inspect the same backend used by every actual handler call.
	db, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	a, b, hidden := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,business_name,is_active) VALUES($1,'A',true),($2,'B',true),($3,'Hidden',false)`, a, b, hidden)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,title,category,created_at) VALUES(gen_random_uuid(),$1,'Plumbing Synthetic A','Repair','2026-01-03'),(gen_random_uuid(),$2,'Plumbing hotel B','Repair','2026-01-02'),(gen_random_uuid(),$3,'Hidden','Repair','2026-01-01')`, a, b, hidden)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	controller := &ProviderMarketplaceController{db: db}
	app.Get("/listings", controller.ListPublicListings)
	type page struct {
		Items []struct {
			ID       string `json:"id"`
			Provider string `json:"provider_id"`
		} `json:"items"`
		Next string `json:"next_cursor"`
	}
	get := func(query string) page {
		t.Helper()
		response, e := app.Test(httptest.NewRequest("GET", "/listings"+query, nil))
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("query %s status %d", query, response.StatusCode)
		}
		var result page
		if e = json.NewDecoder(response.Body).Decode(&result); e != nil {
			t.Fatal(e)
		}
		return result
	}
	for i := 0; i < 10; i++ {
		result := get("?search=Plumbing&category=Repair&limit=20")
		if len(result.Items) != 2 || result.Items[0].Provider != a || result.Items[1].Provider != b {
			t.Fatalf("incorrect eligibility/order after connection reuse: %+v", result)
		}
	}
	for _, tc := range []struct{ search, provider string }{{"Synthetic", a}, {"hotel", b}} {
		result := get("?search=" + tc.search)
		if len(result.Items) != 1 || result.Items[0].Provider != tc.provider {
			t.Fatalf("workload search %s changed: %+v", tc.search, result)
		}
	}
	if result := get("?search=trousers"); len(result.Items) != 0 {
		t.Fatal("nonmatching workload search returned listings")
	}
	first := get("?limit=1")
	if len(first.Items) != 1 || first.Next == "" {
		t.Fatalf("first page missing: %+v", first)
	}
	second := get("?limit=1&cursor=" + url.QueryEscape(first.Next))
	if len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.Items[0].Provider != b {
		t.Fatalf("cursor skipped or duplicated: %+v", second)
	}
	excluded := get("?exclude=" + first.Items[0].ID)
	if len(excluded.Items) != 1 || excluded.Items[0].Provider != b {
		t.Fatalf("exclude changed: %+v", excluded)
	}
	if got := get("?search=unmatched"); len(got.Items) != 0 {
		t.Fatal("search filter lost")
	}
	var named int
	err = db.QueryRow(ctx, `SELECT count(*) FROM pg_prepared_statements WHERE statement LIKE 'SELECT l.id::text,l.provider_id::text,p.business_name,l.listing_type%' OR statement LIKE 'WITH discovery_identity AS MATERIALIZED%'`).Scan(&named)
	if err != nil || named != 0 {
		t.Fatalf("service query used a reusable named statement: count=%d error=%v", named, err)
	}
}
