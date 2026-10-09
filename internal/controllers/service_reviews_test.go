package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestServiceMinimumRating(t *testing.T) {
	for _, raw := range []string{"NaN", "Inf", "-1", "5.1", "oops"} {
		if _, err := serviceMinimumRating(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"", "0", "4", "4.5", "5"} {
		if _, err := serviceMinimumRating(raw); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServiceReviewsOwnershipDiscoveryAndConcurrentTotals(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,full_name text);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text,verification_status text DEFAULT 'approved',is_active boolean DEFAULT true);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,is_active boolean DEFAULT true);
 CREATE TABLE provider_requests(id uuid PRIMARY KEY,listing_id uuid,provider_id uuid,user_id uuid,status text);
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);
 CREATE TABLE countries_config(country_code text,currency_code text,is_active boolean,active_payment_gateways text[]);
 CREATE TABLE xp_transactions(user_id uuid,amount int,reason text,reference_id text UNIQUE);
 CREATE TABLE notifications(user_id uuid,order_id uuid,batch_id uuid,type text,title text,body text,data jsonb,event_key text UNIQUE);
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,listing_type text DEFAULT 'artisan',title text DEFAULT 'Plumber',slug text DEFAULT '',description text DEFAULT '',category text DEFAULT '',address_line text DEFAULT '',city text DEFAULT 'Abuja',state text DEFAULT 'FCT',country_code text DEFAULT 'NG',price numeric DEFAULT 100,currency_code text DEFAULT 'NGN',pricing_unit text DEFAULT 'job',capacity int DEFAULT 1,media_urls text[] DEFAULT '{}',attributes jsonb DEFAULT '{}',status text DEFAULT 'approved',moderation_notes text,published_at timestamptz,created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now(),latitude numeric DEFAULT 9,longitude numeric DEFAULT 7,service_radius_km numeric,is_mobile_service boolean DEFAULT false,is_available_now boolean DEFAULT true);
 INSERT INTO countries_config VALUES('NG','NGN',true,ARRAY['flutterwave']);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"039_marketplace_location_reviews_notifications.sql", "058_service_review_totals.sql"} {
		raw, e := os.ReadFile(filepath.Join("..", "..", "migrations", file))
		if e != nil {
			t.Fatal(e)
		}
		sql := strings.Split(string(raw), "ALTER TABLE user_push_tokens")[0]
		if _, e = db.Exec(ctx, sql); e != nil {
			t.Fatal(file, e)
		}
	}
	provider, buyer, other, listing, request := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO users VALUES($1,'Customer'),($2,'Other')`, buyer, other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,business_name) VALUES($1,'Plumber')`, provider); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id) VALUES($1,$2)`, listing, provider); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_requests VALUES($1,$2,$3,$4,'pending')`, request, listing, provider, buyer); err != nil {
		t.Fatal(err)
	}
	controller := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-Test-User")); return c.Next() })
	app.Put("/listings/:listing_id/review", controller.UpsertListingReview)
	app.Get("/listings/:listing_id/reviews", controller.ListListingReviews)
	app.Get("/listings/:listing_id", controller.GetPublicListing)
	app.Get("/listings", controller.ListPublicListings)
	app.Get("/nearby", controller.ListNearbyListings)
	call := func(method, path, user, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Test-User", user)
		response, e := app.Test(r, 5000)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, raw)
		}
		var result map[string]any
		_ = json.Unmarshal(raw, &result)
		return result
	}
	review := func(rating int, text string) string {
		raw, _ := json.Marshal(map[string]any{"request_id": request, "rating": rating, "review_text": text})
		return string(raw)
	}
	path := "/listings/" + listing + "/review"
	call("PUT", path, buyer, review(5, "Great work"), 403)
	if _, err = db.Exec(ctx, `UPDATE provider_requests SET status='completed' WHERE id=$1`, request); err != nil {
		t.Fatal(err)
	}
	call("PUT", path, other, review(5, "Not my request"), 403)
	if _, err = db.Exec(ctx, `INSERT INTO provider_members VALUES($1,$2,true)`, provider, buyer); err != nil {
		t.Fatal(err)
	}
	call("PUT", path, buyer, review(5, "Own business"), 403)
	if _, err = db.Exec(ctx, `DELETE FROM provider_members`); err != nil {
		t.Fatal(err)
	}
	call("PUT", path, buyer, review(0, "Invalid"), 422)
	call("PUT", path, buyer, review(5, strings.Repeat("x", 1001)), 422)
	if got := call("PUT", path, buyer, review(5, "Great work"), 200); got["xp_awarded"] != float64(reviewRewardXP) {
		t.Fatal(got)
	}
	if got := call("PUT", path, buyer, review(3, "Updated experience"), 200); got["xp_awarded"] != float64(0) {
		t.Fatal(got)
	}
	var count, sum, xp int
	if err = db.QueryRow(ctx, `SELECT review_count,review_rating_sum FROM provider_listings WHERE id=$1`, listing).Scan(&count, &sum); err != nil || count != 1 || sum != 3 {
		t.Fatalf("totals %d/%d %v", count, sum, err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM xp_transactions`).Scan(&xp); err != nil || xp != 1 {
		t.Fatal(xp, err)
	}
	if got := call("GET", "/listings/"+listing, "", "", 200); got["average_rating"] != float64(3) || got["review_count"] != float64(1) {
		t.Fatal(got)
	}
	if got := call("GET", "/listings?min_rating=4", "", "", 200); len(got["items"].([]any)) != 0 {
		t.Fatal(got)
	}
	if got := call("GET", "/nearby?latitude=9&longitude=7&min_rating=4", "", "", 200); len(got["items"].([]any)) != 0 {
		t.Fatal(got)
	}
	call("PUT", path, buyer, review(5, "Excellent"), 200)
	if got := call("GET", "/listings?min_rating=4", "", "", 200); len(got["items"].([]any)) != 1 {
		t.Fatal(got)
	}
	if got := call("GET", "/nearby?latitude=9&longitude=7&min_rating=4", "", "", 200); len(got["items"].([]any)) != 1 {
		t.Fatal(got)
	}
	tx1, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx1.Rollback(ctx)
	tx2, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx2.Rollback(ctx)
	var pid int
	if e = tx2.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); e != nil {
		t.Fatal(e)
	}
	insert := `INSERT INTO provider_listing_reviews(request_id,listing_id,user_id,rating,review_text) VALUES($1,$2,$3,$4,'Concurrent')`
	// Additional requests are created directly for the trigger concurrency case.
	req2, req3 := uuid.NewString(), uuid.NewString()
	for _, id := range []string{req2, req3} {
		if _, e = db.Exec(ctx, `INSERT INTO provider_requests VALUES($1,$2,$3,$4,'completed')`, id, listing, provider, other); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = tx1.Exec(ctx, insert, req2, listing, other, 4); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := tx2.Exec(ctx, insert, req3, listing, other, 2); done <- e }()
	deadline := time.Now().Add(3 * time.Second)
	locked := false
	for time.Now().Before(deadline) {
		if e = db.QueryRow(ctx, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&locked); e != nil {
			t.Fatal(e)
		}
		if locked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !locked {
		t.Fatal("second review did not reach concurrent listing lock")
	}
	if e = tx1.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if e = tx2.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(ctx, `SELECT review_count,review_rating_sum FROM provider_listings WHERE id=$1`, listing).Scan(&count, &sum); e != nil || count != 3 || sum != 11 {
		t.Fatalf("concurrent totals %d/%d %v", count, sum, e)
	}
	page := call("GET", "/listings/"+listing+"/reviews?limit=1", "", "", 200)
	next, ok := page["next_cursor"].(string)
	if !ok || next == "" {
		t.Fatal(page)
	}
	page2 := call("GET", "/listings/"+listing+"/reviews?limit=1&cursor="+next, "", "", 200)
	if page["items"].([]any)[0].(map[string]any)["id"] == page2["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("review pagination repeated first item")
	}
	if _, e = db.Exec(ctx, `DELETE FROM provider_listing_reviews WHERE request_id=$1`, req2); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(ctx, `SELECT review_count,review_rating_sum FROM provider_listings WHERE id=$1`, listing).Scan(&count, &sum); e != nil || count != 2 || sum != 7 {
		t.Fatalf("delete totals %d/%d %v", count, sum, e)
	}
}
