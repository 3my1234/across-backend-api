package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiscoveryRankingDiversityAndRelevance(t *testing.T) {
	items := []discoveryItem{{Kind: "product", ID: "a", Provider: "one", Paid: true, Promote: true, Tier: "bronze", Priority: 1, Score: 40}, {Kind: "service", ID: "b", Provider: "two", Paid: true, Promote: true, Tier: "diamond", Priority: 1, Score: 40}, {Kind: "product", ID: "c", Provider: "one", Paid: true, Promote: true, Tier: "diamond", Priority: 3, Score: 1}, {Kind: "service", ID: "d", Provider: "free", Promote: true, Priority: 1, Score: 100}}
	ranked := rankDiscovery(items, "similar", "same", 8)
	if len(ranked) != 3 || ranked[0].ID != "d" {
		t.Fatal(ranked)
	}
	for _, item := range rankDiscovery(items, "promoted", "same", 8) {
		if !item.Paid || item.Provider == "free" || item.Score < 40 {
			t.Fatal("irrelevant paid priority or repeat provider", item)
		}
	}
	diamond := 0
	for i := 0; i < 1000; i++ {
		if rankDiscovery(items[:2], "promoted", fmt.Sprint(i), 1)[0].Tier == "diamond" {
			diamond++
		}
	}
	if diamond < 650 || diamond > 950 {
		t.Fatalf("weighted opportunity: %d", diamond)
	}
	if rankDiscovery(items, "promoted", "stable", 1)[0].ID != rankDiscovery(items, "promoted", "stable", 1)[0].ID {
		t.Fatal("unstable refresh")
	}
	if discoveryRelevance(discoveryItem{Category: []string{"Phones"}, Title: "Phone repair", City: "Abuja"}, []string{"Phones"}, discoveryWords("Phone"), "Abuja") <= discoveryRelevance(discoveryItem{Title: "Clothes", City: "Abuja"}, []string{"Phones"}, discoveryWords("Phone"), "Abuja") {
		t.Fatal("context lost")
	}
}
func discoveryFixture(t *testing.T) (*pgxpool.Pool, *DiscoveryController, *fiber.App) {
	t.Helper()
	db := communityTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `ALTER TABLE products ADD COLUMN description text DEFAULT '',ADD COLUMN inventory_count int DEFAULT 5,ADD COLUMN payment_mode_unused text,ADD COLUMN fulfillment_mode text DEFAULT 'merchant_local',ADD COLUMN inventory_city text DEFAULT 'Abuja',ADD COLUMN created_at timestamptz DEFAULT now();
 ALTER TABLE product_delivery_areas ADD COLUMN state_key text DEFAULT '',ADD COLUMN delivered_price numeric DEFAULT 100,ADD COLUMN currency_code text DEFAULT 'NGN';
 ALTER TABLE provider_listings ADD COLUMN listing_type text DEFAULT 'artisan',ADD COLUMN description text DEFAULT '',ADD COLUMN price numeric DEFAULT 100,ADD COLUMN currency_code text DEFAULT 'NGN',ADD COLUMN is_available_now boolean DEFAULT true,ADD COLUMN created_at timestamptz DEFAULT now();
 CREATE TABLE countries_config(country_code text,currency_code text,is_active boolean,active_payment_gateways text[]);
 INSERT INTO countries_config VALUES('NG','NGN',true,ARRAY['flutterwave']);`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/103_listing_discovery.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	d := NewDiscoveryController(db, config.Config{JWTSecret: "isolated-discovery-test-key-never-production"})
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Get("/discovery", d.Feed)
	app.Post("/events", d.Events)
	app.Get("/provider", d.ProviderSummary)
	app.Put("/provider", d.ProviderSettings)
	return db, d, app
}
func TestDiscoveryPostgresEligibilityControlsAndMetrics(t *testing.T) {
	db, d, app := discoveryFixture(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	providerIDs, users, products, services := []string{}, []string{}, []string{}, []string{}
	plan := uuid.NewString()
	exec(`INSERT INTO provider_subscription_plans(id,code,listing_limit,badge_tier) VALUES($1,'provider-diamond',40,'diamond')`, plan)
	for i := 0; i < 12; i++ {
		user, pid, product, service, sub := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
		providerIDs = append(providerIDs, pid)
		users = append(users, user)
		products = append(products, product)
		services = append(services, service)
		exec(`INSERT INTO users(id,full_name) VALUES($1,'Provider user')`, user)
		exec(`INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status) VALUES($1,$2,'Phone business','approved')`, pid, user)
		exec(`INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner')`, pid, user)
		if i > 0 {
			exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,current_period_end) VALUES($1,$2,$3,'active',now()+interval '1 month')`, sub, pid, plan)
			exec(`INSERT INTO provider_subscription_payments VALUES($1,500,'NGN')`, sub)
		}
		exec(`INSERT INTO products(id,provider_id,title,category_path,is_active,moderation_status,published_at) VALUES($1,$2,'Phone for sale',ARRAY['Electronics','Phones'],true,'approved',now())`, product, pid)
		exec(`INSERT INTO product_delivery_areas(product_id,country_code) VALUES($1,'NG')`, product)
		if i > 0 {
			exec(`INSERT INTO provider_listings(id,provider_id,title,category,status,published_at) VALUES($1,$2,'Phone repair','Phones','approved',CASE WHEN $3 THEN now() ELSE NULL END)`, service, pid, i > 0)
		}

	}
	session := uuid.NewString()
	feed := func() map[string]any {
		return communityCall(t, app, "GET", "/discovery?country_code=NG&session="+session+"&source_kind=product&source_id="+products[0], "", "", 200)
	}
	result := feed()
	if len(result["similar"].([]any)) == 0 || len(result["promoted"].([]any)) == 0 {
		t.Fatal("missing rails", result)
	}
	seen := map[string]bool{}
	var ad map[string]any
	for _, placement := range []string{"similar", "promoted"} {
		businesses := map[string]bool{}
		for _, raw := range result[placement].([]any) {
			card := raw.(map[string]any)
			id, pid := card["id"].(string), card["provider_id"].(string)
			if id == products[0] || seen[id] || businesses[pid] {
				t.Fatal("duplicate/self/provider monopoly", card)
			}
			seen[id] = true
			businesses[pid] = true
			if placement == "promoted" {
				if card["label"] != "Promoted" || pid == providerIDs[0] {
					t.Fatal("free promoted", card)
				}
				ad = card
			}
		}
	}
	var counts int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM discovery_daily_metrics`).Scan(&counts); err != nil || counts != 0 {
		t.Fatal("serving counted impression", counts, err)
	}
	receipt := ad["receipt"].(string)
	send := func(receipt, kind string, ms, status int) {
		t.Helper()
		payload, _ := json.Marshal(fiber.Map{"events": []fiber.Map{{"receipt": receipt, "type": kind, "visible_ms": ms}}})
		communityCall(t, app, "POST", "/events", "", string(payload), status)
	}
	send(receipt, "impression", 999, 204)
	send(receipt, "impression", 1000, 204)
	send(receipt, "impression", 5000, 204)
	send(receipt, "click", 0, 204)
	send(receipt, "detail_open", 0, 204)
	send(receipt, "contact_tap", 0, 204)
	send(receipt+"tampered", "click", 0, 401)
	expired, _ := d.readReceipt(receipt)
	expired.Issued = time.Now().Add(-3 * time.Hour).Unix()
	send(d.signReceipt(expired), "click", 0, 401)
	if err := db.QueryRow(ctx, `SELECT sum(impressions)::int FROM discovery_daily_metrics`).Scan(&counts); err != nil || counts != 1 {
		t.Fatal("impression dedup", counts, err)
	}
	// The same daily session cannot inflate counters by fetching fresh receipts.
	var owner string
	for i, pid := range providerIDs {
		if pid == ad["provider_id"] {
			owner = users[i]
		}
	}
	summary := communityCall(t, app, "GET", "/provider", owner, "", 200)
	if summary["metrics"].(map[string]any)["impressions"] != float64(1) {
		t.Fatal(summary)
	}
	other := communityCall(t, app, "GET", "/provider", users[0], "", 200)
	if other["metrics"].(map[string]any)["impressions"] != float64(0) {
		t.Fatal("private business analytics leaked")
	}
	communityCall(t, app, "PUT", "/provider", users[0], `{"kind":"product","listing_id":"`+ad["id"].(string)+`","enabled":false,"priority":1}`, 404)
	communityCall(t, app, "PUT", "/provider", owner, `{"enabled":false}`, 204)
	for _, raw := range feed()["promoted"].([]any) {
		if raw.(map[string]any)["provider_id"] == ad["provider_id"] {
			t.Fatal("opt-out ignored")
		}
	}
	communityCall(t, app, "PUT", "/provider", owner, `{}`, 400)
	exec(`UPDATE products SET inventory_count=0 WHERE id=$1`, products[1])
	exec(`UPDATE provider_listings SET is_available_now=false WHERE id=$1`, services[1])
	exec(`UPDATE provider_organizations SET is_active=false WHERE id=$1`, providerIDs[2])
	exec(`UPDATE products SET moderation_status='pending' WHERE id=$1`, products[3])
	exec(`UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE provider_id=$1`, providerIDs[4])
	for placement, rail := range feed() {
		for _, raw := range rail.([]any) {
			item := raw.(map[string]any)
			if item["id"] == products[1] || item["id"] == services[1] || item["provider_id"] == providerIDs[2] || item["id"] == products[3] {
				t.Fatal("inactive listing", item)
			}
			if placement == "promoted" && item["provider_id"] == providerIDs[4] {
				t.Fatal("expired paid listing promoted")
			}
		}
	}
	exec(`INSERT INTO discovery_event_dedup VALUES('expired',now()-interval '1 hour')`)
	if err := PurgeDiscoveryMetrics(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM discovery_event_dedup WHERE event_key='expired'`).Scan(&counts); err != nil || counts != 0 {
		t.Fatal("retention", err)
	}
	communityCall(t, app, "GET", "/discovery?session=invalid", "", "", 422)
	// Empty markets return empty rails, not unrelated cross-border inventory.
	exec(`UPDATE countries_config SET is_active=false`)
	result = communityCall(t, app, "GET", "/discovery?country_code=NG&session="+session, "", "", 200)
	if len(result["recommended"].([]any))+len(result["promoted"].([]any)) != 0 {
		t.Fatal(result)
	}
}

func TestDiscoveryReceiptValidation(t *testing.T) {
	d := &DiscoveryController{key: []byte("isolated-test-signing-key-32-bytes")}
	r := discoveryReceipt{"product", uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64), "promoted", time.Now().Unix()}
	token := d.signReceipt(r)
	if _, err := d.readReceipt(token); err != nil {
		t.Fatal(err)
	}
	if _, err := d.readReceipt(token + "x"); err == nil {
		t.Fatal("tampered receipt accepted")
	}
	app := fiber.New()
	app.Post("/events", d.Events)
	response, err := app.Test(httptest.NewRequest("POST", "/events", strings.NewReader(`{}`)))
	if err != nil || response.StatusCode != 400 {
		t.Fatal(err)
	}
	response.Body.Close()
}
