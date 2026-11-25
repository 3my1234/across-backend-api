package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestFlightRouteValidation(t *testing.T) {
	ok := func() flightRoute {
		return flightRoute{Type: "Local", From: "  Abuja ", To: "Port  Harcourt", Price: 140000}
	}
	route := ok()
	if err := validFlightRoute(&route); err != nil || route.Type != "local" || route.From != "Abuja" || route.To != "Port Harcourt" {
		t.Fatal(route, err)
	}
	price := func(v float64) *float64 { return &v }
	route = ok()
	route.Discount = price(0)
	if err := validFlightRoute(&route); err != nil || route.Discount != nil {
		t.Fatal("an empty discount is no discount", err)
	}
	for name, change := range map[string]func(*flightRoute){
		"type":         func(r *flightRoute) { r.Type = "space" },
		"from":         func(r *flightRoute) { r.From = "1" },
		"markup":       func(r *flightRoute) { r.To = "<b>Lagos</b>" },
		"digits":       func(r *flightRoute) { r.To = "Call 0803" },
		"punctuation":  func(r *flightRoute) { r.To = "..." },
		"same":         func(r *flightRoute) { r.To = "abuja" },
		"zero":         func(r *flightRoute) { r.Price = 0 },
		"negative":     func(r *flightRoute) { r.Price = -1 },
		"huge":         func(r *flightRoute) { r.Price = 1e12 },
		"not cheaper":  func(r *flightRoute) { r.Discount = price(140000) },
		"rounds to it": func(r *flightRoute) { r.Discount = price(139999.999) },
		"bad discount": func(r *flightRoute) { r.Discount = price(-5) },
	} {
		route = ok()
		change(&route)
		if validFlightRoute(&route) == nil {
			t.Fatal("accepted a bad route:", name)
		}
	}
	text := flightRequest{Type: "local", From: "Abuja", To: "Lagos", Price: 140000, Discount: price(125000), Currency: "NGN", TravelDate: "2026-12-12", Passengers: 2}.text("Morning please")
	if text != "Abuja to Lagos (local flight), 12 December 2026, 2 passengers, NGN 125,000 each. Morning please" {
		t.Fatal(text)
	}
	if flightMoney("NGN", 999) != "NGN 999" || flightMoney("NGN", 1250000) != "NGN 1,250,000" {
		t.Fatal("money")
	}
}

func TestFlightCardsEndToEnd(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	must := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, query, args...); err != nil {
			t.Fatal(err, query[:min(len(query), 90)])
		}
	}
	must(`CREATE TABLE users(id uuid PRIMARY KEY,full_name text,email text,phone text);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text,is_active boolean DEFAULT true,verification_status text DEFAULT 'approved');
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean DEFAULT true,created_at timestamptz DEFAULT now());
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,title text,category text DEFAULT '',listing_type text DEFAULT 'artisan',status text DEFAULT 'approved',country_code text DEFAULT 'NG',currency_code text DEFAULT 'NGN',plan_slot integer DEFAULT 1);
 CREATE TABLE countries_config(country_code text,currency_code text,is_active boolean,active_payment_gateways text[]);
 INSERT INTO countries_config VALUES('NG','NGN',true,ARRAY['flutterwave']);
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);
 CREATE TABLE products(id uuid PRIMARY KEY,title text,plan_slot integer DEFAULT 1);
 CREATE TABLE support_tickets(id uuid PRIMARY KEY,status text,updated_at timestamptz);
 CREATE TABLE provider_listing_reviews(request_id uuid,rating int,review_text text);
 CREATE TABLE notifications(user_id uuid,type text,title text,body text,data jsonb,event_key text);
 CREATE UNIQUE INDEX ON notifications(event_key) WHERE event_key IS NOT NULL;
 CREATE TABLE provider_marketplace_events(provider_id uuid,listing_id uuid,event_type text,metadata jsonb);
 CREATE TABLE provider_requests(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),listing_id uuid,provider_id uuid,user_id uuid,request_type text,message text DEFAULT '',created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now(),status text DEFAULT 'pending',slot_id uuid,starts_at timestamptz,ends_at timestamptz,party_size int DEFAULT 1,search_text text DEFAULT '',idempotency_key text,UNIQUE(user_id,idempotency_key))`)
	migrate := func(names ...string) {
		for _, name := range names {
			raw, err := os.ReadFile("../../migrations/" + name)
			if err != nil {
				t.Fatal(err)
			}
			must(string(raw))
		}
	}
	migrate("041_provider_conversations.sql")
	must(`ALTER TABLE provider_conversations ADD COLUMN product_id uuid;ALTER TABLE provider_conversation_messages ADD COLUMN media_keys text[] DEFAULT '{}'`)
	migrate("063_request_conversations_and_support_history.sql", "074_chat_history_revision.sql")
	installPublicIdentityFixture(t, db)

	buyer, owner, staff, stranger, provider, rival := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	flights, plumbing, rivalFlights := uuid.NewString(), uuid.NewString(), uuid.NewString()
	must(`INSERT INTO users VALUES($1,'Buyer','b@example.test',''),($2,'Owner','o@example.test',''),($3,'Staff','s@example.test',''),($4,'Rival','r@example.test','')`, buyer, owner, staff, stranger)
	must(`INSERT INTO provider_organizations(id,business_name) VALUES($1,'Sky Travels'),($2,'Rival')`, provider, rival)
	must(`INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner'),($1,$3,'staff'),($4,$5,'owner')`, provider, owner, staff, rival, stranger)
	must(`INSERT INTO provider_listings(id,provider_id,title,category) VALUES($1,$4,'Sky Travels','Flight booking'),($2,$4,'Pipes fixed','Plumbing'),($3,$5,'Other','Travel agency services')`, flights, plumbing, rivalFlights, provider, rival)

	m := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Get("/l/:listing_id/flights", m.ListFlights)
	app.Get("/me/:listing_id/flights", m.MyFlights)
	app.Post("/me/:listing_id/flights", m.SaveFlight)
	app.Put("/me/:listing_id/flights/:route_id", m.SaveFlight)
	app.Delete("/me/:listing_id/flights/:route_id", m.DeleteFlight)
	app.Post("/l/:listing_id/requests", m.CreateRequest)
	app.Get("/me/requests", m.ListProviderRequests)
	app.Patch("/me/requests/:request_id", m.UpdateProviderRequest)
	app.Get("/buyer/:conversation_id", m.BuyerConversationMessages)

	// Before the migration everything that exists today keeps working.
	plain := communityCall(t, app, "POST", "/l/"+plumbing+"/requests", buyer, `{"request_type":"booking","message":"Leak","idempotency_key":"old"}`, 201)
	communityCall(t, app, "PATCH", "/me/requests/"+plain["id"].(string), owner, `{"status":"accepted"}`, 204)
	if chat := communityCall(t, app, "GET", "/buyer/"+plain["conversation_id"].(string), buyer, "", 200); len(chat["items"].([]any)) != 1 {
		t.Fatal("an ordinary request must not add a chat answer", chat)
	}
	communityCall(t, app, "GET", "/me/requests", owner, "", 200)
	migrate("130_flight_routes.sql")

	// Who may edit, and only on flight services.
	good := `{"flight_type":"local","from_city":"Abuja","to_city":"Lagos","price":140000,"discount_price":125000}`
	for actor, want := range map[string]int{staff: 403, buyer: 403, stranger: 404} {
		communityCall(t, app, "POST", "/me/"+flights+"/flights", actor, good, want)
		communityCall(t, app, "GET", "/me/"+flights+"/flights", actor, "", want)
	}
	communityCall(t, app, "POST", "/me/"+plumbing+"/flights", owner, good, 422)
	communityCall(t, app, "POST", "/me/"+flights+"/flights", owner, `{"flight_type":"local","from_city":"Abuja","to_city":"Lagos","price":140000,"discount_price":150000}`, 422)
	route := communityCall(t, app, "POST", "/me/"+flights+"/flights", owner, good, 200)["id"].(string)
	communityCall(t, app, "POST", "/me/"+flights+"/flights", owner, strings.Replace(good, "Lagos", " LAGOS ", 1), 409)
	abroad := communityCall(t, app, "POST", "/me/"+flights+"/flights", owner, `{"flight_type":"international","from_city":"Abuja","to_city":"Addis Ababa","price":850000}`, 200)["id"].(string)
	foreign := communityCall(t, app, "POST", "/me/"+rivalFlights+"/flights", stranger, good, 200)["id"].(string)
	communityCall(t, app, "PUT", "/me/"+flights+"/flights/"+foreign, owner, good, 404)

	// Customers see active routes; hiding one removes it, and listing review is untouched.
	list := communityCall(t, app, "GET", "/l/"+flights+"/flights", "", "", 200)["items"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["flight_type"] != "local" || list[0].(map[string]any)["discount_price"].(float64) != 125000 {
		t.Fatal(list)
	}
	communityCall(t, app, "PUT", "/me/"+flights+"/flights/"+abroad, owner, `{"flight_type":"international","from_city":"Abuja","to_city":"Addis Ababa","price":850000,"is_active":false}`, 200)
	if list = communityCall(t, app, "GET", "/l/"+flights+"/flights", "", "", 200)["items"].([]any); len(list) != 1 {
		t.Fatal("hidden route is public")
	}
	if list = communityCall(t, app, "GET", "/me/"+flights+"/flights", owner, "", 200)["items"].([]any); len(list) != 2 {
		t.Fatal("the provider must still see a hidden route")
	}
	if list = communityCall(t, app, "GET", "/l/"+plumbing+"/flights", "", "", 200)["items"].([]any); len(list) != 0 {
		t.Fatal("only flight services have flights")
	}
	var status string
	if err := db.QueryRow(ctx, `SELECT status FROM provider_listings WHERE id=$1`, flights).Scan(&status); err != nil || status != "approved" {
		t.Fatal("changing a price sent the listing to review")
	}

	// A request: the price always comes from the provider, never from the customer.
	day := time.Now().UTC().AddDate(0, 0, 14).Format("2006-01-02")
	ask := func(route, date, key string, people, want int) map[string]any {
		return communityCall(t, app, "POST", "/l/"+flights+"/requests", buyer, fmt.Sprintf(`{"flight_route_id":%q,"travel_date":%q,"party_size":%d,"message":"Morning please","price":1,"idempotency_key":%q}`, route, date, people, key), want)
	}
	made := ask(route, day, "a", 2, 201)
	if again := ask(route, day, "a", 2, 200); again["id"] != made["id"] {
		t.Fatal("a retry made a second request")
	}
	ask(abroad, day, "hidden", 1, 409)
	ask(foreign, day, "foreign", 1, 409)
	ask(route, "2020-01-01", "past", 1, 422)
	ask(route, time.Now().UTC().AddDate(2, 0, 0).Format("2006-01-02"), "far", 1, 422)
	ask(route, "tomorrow", "words", 1, 422)
	ask(route, day, "crowd", 10, 422)
	ask("x", day, "bad", 1, 422)
	communityCall(t, app, "POST", "/l/"+flights+"/requests", owner, fmt.Sprintf(`{"flight_route_id":%q,"travel_date":%q,"party_size":1,"idempotency_key":"self"}`, route, day), 403)

	requestID, conversation := made["id"].(string), made["conversation_id"].(string)
	card := func() map[string]any {
		t.Helper()
		items := communityCall(t, app, "GET", "/buyer/"+conversation, buyer, "", 200)["items"].([]any)
		for _, item := range items {
			if found, ok := item.(map[string]any)["flight_request"].(map[string]any); ok && found["request_id"] == requestID {
				return found
			}
		}
		t.Fatal("no card in the chat", items)
		return nil
	}
	first := card()
	flight := first["flight"].(map[string]any)
	if first["status"] != "pending" || first["note"] != "Morning please" || flight["price"].(float64) != 140000 || flight["discount_price"].(float64) != 125000 || flight["passengers"].(float64) != 2 || flight["travel_date"] != day {
		t.Fatal(first)
	}
	// A later price change or deletion never rewrites what was asked.
	communityCall(t, app, "PUT", "/me/"+flights+"/flights/"+route, owner, `{"flight_type":"local","from_city":"Abuja","to_city":"Lagos","price":200000}`, 200)
	if card()["flight"].(map[string]any)["price"].(float64) != 140000 {
		t.Fatal("the request changed with the route")
	}
	rows := communityCall(t, app, "GET", "/me/requests", owner, "", 200)["items"].([]any)
	found := false
	for _, row := range rows {
		item := row.(map[string]any)
		if item["id"] == requestID {
			found = item["flight"].(map[string]any)["to_city"] == "Lagos"
		} else if item["flight"] != nil {
			t.Fatal("an ordinary request has no flight", item)
		}
	}
	if !found {
		t.Fatal("the request is missing from Bookings")
	}

	// The answer reaches the chat once, refreshes the card and cannot be repeated.
	var before, after int64
	revision := func(into *int64) {
		if err := db.QueryRow(ctx, `SELECT history_revision FROM provider_conversations WHERE id=$1`, conversation).Scan(into); err != nil {
			t.Fatal(err)
		}
	}
	revision(&before)
	communityCall(t, app, "PATCH", "/me/requests/"+requestID, stranger, `{"status":"accepted"}`, 409)
	communityCall(t, app, "PATCH", "/me/requests/"+requestID, owner, `{"status":"accepted"}`, 204)
	communityCall(t, app, "PATCH", "/me/requests/"+requestID, owner, `{"status":"accepted"}`, 409)
	revision(&after)
	if after <= before || card()["status"] != "accepted" {
		t.Fatal("the chat did not refresh")
	}
	var answers int
	var notified string
	if err := db.QueryRow(ctx, `SELECT count(*) FROM provider_conversation_messages WHERE conversation_id=$1 AND sender_type='provider' AND body LIKE 'Abuja to Lagos is available.%'`, conversation).Scan(&answers); err != nil || answers != 1 {
		t.Fatal("answers", answers, err)
	}
	if err := db.QueryRow(ctx, `SELECT body FROM notifications WHERE user_id=$1 AND event_key=$2`, buyer, "provider-request-status:"+requestID+":accepted").Scan(&notified); err != nil || !strings.Contains(notified, "Abuja to Lagos is available") {
		t.Fatal(notified, err)
	}
	communityCall(t, app, "DELETE", "/me/"+flights+"/flights/"+route, owner, "", 204)
	if card()["flight"].(map[string]any)["to_city"] != "Lagos" {
		t.Fatal("deleting the route lost the request")
	}

	// A customer cannot flood a provider, even with many requests at once.
	busy := communityCall(t, app, "POST", "/me/"+flights+"/flights", owner, `{"flight_type":"local","from_city":"Abuja","to_city":"Kano","price":90000}`, 200)["id"].(string)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := db.Begin(ctx)
			if err != nil {
				return
			}
			defer tx.Rollback(ctx)
			built, err := buildFlightRequest(ctx, tx, flights, buyer, busy, day, 1, time.Now())
			if err != nil {
				return
			}
			raw, _ := json.Marshal(built)
			if _, err = tx.Exec(ctx, `INSERT INTO provider_requests(listing_id,provider_id,user_id,request_type,idempotency_key,flight) VALUES($1,$2,$3,'booking',$4,$5::jsonb)`, flights, provider, buyer, fmt.Sprint("flood", i), raw); err == nil {
				_ = tx.Commit(ctx)
			}
		}(i)
	}
	wg.Wait()
	var waiting int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM provider_requests WHERE user_id=$1 AND flight IS NOT NULL AND status='pending'`, buyer).Scan(&waiting); err != nil || waiting != flightPendingLimit {
		t.Fatal("pending limit", waiting, err)
	}

	// The number of routes is bounded.
	must(`INSERT INTO provider_flight_routes(listing_id,flight_type,from_city,to_city,price) SELECT $1,'local','Abuja','Town '||chr(65+n/26)||chr(65+n%26),1000 FROM generate_series(1,70) n LIMIT (SELECT $2-count(*) FROM provider_flight_routes WHERE listing_id=$1)`, flights, flightRouteLimit)
	communityCall(t, app, "POST", "/me/"+flights+"/flights", owner, `{"flight_type":"local","from_city":"Enugu","to_city":"Owerri","price":50000}`, 422)

	// A suspended provider or rejected listing shows nothing.
	must(`UPDATE provider_listings SET status='pending' WHERE id=$1`, flights)
	if list = communityCall(t, app, "GET", "/l/"+flights+"/flights", "", "", 200)["items"].([]any); len(list) != 0 {
		t.Fatal("an unapproved listing shows flights")
	}
}
