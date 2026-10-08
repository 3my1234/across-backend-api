package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestRequestConversationBackfillAndAtomicBooking(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,full_name text,email text,phone text);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text,is_active boolean DEFAULT true,verification_status text DEFAULT 'approved');
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean DEFAULT true,created_at timestamptz DEFAULT now());
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,title text,listing_type text DEFAULT 'car_wash',status text DEFAULT 'approved',country_code text DEFAULT 'NG',currency_code text DEFAULT 'NGN');
 CREATE TABLE countries_config(country_code text,currency_code text,is_active boolean,active_payment_gateways text[]);
 INSERT INTO countries_config VALUES('NG','NGN',true,ARRAY['flutterwave']);
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);
 CREATE TABLE provider_requests(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),listing_id uuid,provider_id uuid,user_id uuid,request_type text,message text,created_at timestamptz DEFAULT now(),status text DEFAULT 'pending',slot_id uuid,starts_at timestamptz,ends_at timestamptz,party_size int,search_text text,idempotency_key text,UNIQUE(user_id,idempotency_key));
 CREATE TABLE support_tickets(id uuid PRIMARY KEY,status text,updated_at timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	buyer, owner, provider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	first, second, third, thread := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, file := range []string{"041_provider_conversations.sql"} {
		sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(ctx, `INSERT INTO users VALUES($1,'Buyer','buyer@example.test',''),($2,'Owner','owner@example.test','')`, buyer, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,business_name) VALUES($1,'Repairs')`, provider); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner')`, provider, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,title) VALUES($1,$4,'First'),($2,$4,'Second'),($3,$4,'Third')`, first, second, third, provider); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_conversations(id,listing_id,provider_id,user_id,status,provider_last_read_at,last_message_at) VALUES($1,$2,$3,$4,'blocked','2026-01-01','2026-01-01')`, thread, first, provider, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_conversation_messages(conversation_id,sender_type,sender_user_id,body,created_at) VALUES($1,'provider',$2,'Existing reply','2026-01-01')`, thread, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_requests(listing_id,provider_id,user_id,request_type,message,created_at) VALUES($1,$3,$4,'booking','Fix my car','2026-01-02'),($1,$3,$4,'appointment','Another visit','2026-01-03'),($2,$3,$4,'enquiry','Details please','2026-01-02')`, first, second, provider, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE products(id uuid PRIMARY KEY,title text);ALTER TABLE provider_conversations ADD COLUMN product_id uuid;ALTER TABLE provider_conversation_messages ADD COLUMN media_keys text[] DEFAULT '{}';`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "063_request_conversations_and_support_history.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		if _, err = db.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	var messages int
	var status string
	var preserved, newest bool
	if err = db.QueryRow(ctx, `SELECT status,provider_last_read_at='2026-01-01',last_message_at='2026-01-03' FROM provider_conversations WHERE id=$1`, thread).Scan(&status, &preserved, &newest); err != nil {
		t.Fatal(err)
	}
	if status != "blocked" || !preserved || !newest {
		t.Fatal("backfill changed existing conversation state")
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM provider_conversation_messages WHERE source_request_id IS NOT NULL`).Scan(&messages); err != nil || messages != 3 {
		t.Fatal("backfill duplicated or omitted requests", messages, err)
	}
	var text string
	if err = db.QueryRow(ctx, `SELECT body FROM provider_conversation_messages WHERE body LIKE 'Booking request:%'`).Scan(&text); err != nil || text != "Booking request: Fix my car" {
		t.Fatal(text, err)
	}
	controller := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Post("/listings/:listing_id/requests", controller.CreateRequest)
	app.Get("/buyer/chats", controller.ListBuyerConversations)
	app.Get("/provider/chats", controller.ListProviderConversations)
	for n := 0; n < 2; n++ {
		req := httptest.NewRequest("POST", "/listings/"+third+"/requests", strings.NewReader(`{"request_type":"booking","message":"Book today","idempotency_key":"same-booking"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User", buyer)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 201 && res.StatusCode != 200 {
			t.Fatal("booking failed", res.StatusCode)
		}
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM provider_conversation_messages WHERE source_request_id IS NOT NULL`).Scan(&messages); err != nil || messages != 4 {
		t.Fatal("booking replay duplicated a chat message", messages, err)
	}
	for _, tc := range []struct {
		path, user string
		count      int
	}{{"/buyer/chats", buyer, 3}, {"/provider/chats", owner, 3}, {"/buyer/chats", owner, 0}} {
		cursor := ""
		seen := map[string]bool{}
		for n := 0; n < 5; n++ {
			req := httptest.NewRequest("GET", tc.path+"?limit=1&cursor="+cursor, nil)
			req.Header.Set("X-User", tc.user)
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Items []struct{ ID string }
				Next  string `json:"next_cursor"`
			}
			err = json.NewDecoder(res.Body).Decode(&body)
			res.Body.Close()
			if err != nil || res.StatusCode != 200 {
				t.Fatal(res.StatusCode, err)
			}
			for _, item := range body.Items {
				if seen[item.ID] {
					t.Fatal("duplicate conversation page")
				}
				seen[item.ID] = true
			}
			cursor = body.Next
			if cursor == "" {
				break
			}
		}
		if len(seen) != tc.count {
			t.Fatal("contact history missing or leaked", tc.path, len(seen), tc.count)
		}
	}
	// Request and conversation changes share a transaction and must roll back together.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reqID := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO provider_requests(id,listing_id,provider_id,user_id,request_type,message) VALUES($1,$2,$3,$4,'booking','Rollback')`, reqID, third, provider, owner)
	if err != nil {
		t.Fatal(err)
	}
	rollbackThread, err := linkRequestConversation(ctx, tx, reqID)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	var exists bool
	err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_conversations WHERE id=$1)`, rollbackThread).Scan(&exists)
	if err != nil || exists {
		t.Fatal("rollback left an orphan conversation", err)
	}
}
