package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestProductChatGroupedHistoryMigrationAndReceipts(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,full_name text,email text,is_active boolean DEFAULT true,avatar_url text DEFAULT '');
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text,logo_url text DEFAULT '',owner_user_id uuid,verification_status text DEFAULT 'approved',is_active boolean DEFAULT true);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,is_active boolean DEFAULT true,role text,created_at timestamptz DEFAULT now());
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,title text,status text DEFAULT 'approved',plan_slot integer DEFAULT 1);
 CREATE TABLE products(id uuid PRIMARY KEY,provider_id uuid,title text,is_active boolean DEFAULT true,moderation_status text DEFAULT 'approved',plan_slot integer DEFAULT 1);
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);
 CREATE TABLE provider_marketplace_events(id uuid DEFAULT gen_random_uuid(),provider_id uuid,listing_id uuid,event_type text,metadata jsonb);
 CREATE TABLE chat_messages(id uuid PRIMARY KEY,room_id uuid,sender_user_id uuid,body text,created_at timestamptz DEFAULT now());
 CREATE TABLE chat_rooms(id uuid PRIMARY KEY,kind text,last_message_at timestamptz,title text,state text,context jsonb);
 CREATE TABLE chat_participants(room_id uuid,user_id uuid,state text,last_read_at timestamptz);
 CREATE TABLE notifications(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid,order_id uuid,batch_id uuid,type text,title text,body text,data jsonb,event_key text);
 CREATE UNIQUE INDEX notifications_event_key_unique ON notifications(event_key) WHERE event_key IS NOT NULL;
 CREATE TABLE messaging_preferences(actor_type text,actor_id uuid,read_receipts_enabled boolean,PRIMARY KEY(actor_type,actor_id));
 CREATE TABLE message_receipts(channel text,conversation_id uuid,message_id uuid,recipient_type text,recipient_id uuid,delivered_at timestamptz DEFAULT now(),read_at timestamptz,PRIMARY KEY(channel,message_id,recipient_type,recipient_id));`)
	if err != nil {
		t.Fatal(err)
	}
	apply := func(name string) {
		t.Helper()
		raw, e := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(ctx, string(raw)); e != nil {
			t.Fatal(name, e)
		}
	}
	for _, name := range []string{"041_provider_conversations.sql", "064_product_payment_options_and_chat_images.sql", "074_chat_history_revision.sql", "088_chat_location_pins.sql", "073_provider_web_push.sql", "076_web_push_destinations.sql", "085_provider_native_notifications.sql", "090_provider_notification_conflict_target.sql"} {
		apply(name)
	}
	buyer, owner, other, provider := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	first, second, service, oldA, oldB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO users(id,full_name,email,avatar_url)VALUES($1,'Customer','buyer@example.invalid','user-uploads/buyer/avatar.png'),($2,'Owner','owner@example.invalid',''),($3,'Other','other@example.invalid','');
 INSERT INTO provider_organizations(id,business_name,logo_url,owner_user_id)VALUES($4,'Business','user-uploads/providers/logo/photo.png',$2);
 INSERT INTO provider_members(provider_id,user_id,role)VALUES($4,$2,'owner');
 INSERT INTO products(id,provider_id,title,payment_mode)VALUES($5,$4,'Rice','contact'),($6,$4,'Shoes','contact');
 INSERT INTO provider_listings(id,provider_id,title)VALUES($7,$4,'Delivery');
 INSERT INTO provider_conversations(id,provider_id,user_id,product_id,created_at)VALUES($8,$4,$1,$5,'2025-01-01'),($9,$4,$1,$6,'2025-02-01');
 INSERT INTO provider_conversation_messages(conversation_id,sender_type,sender_user_id,body,created_at)VALUES($8,'provider',$2,'Rice history','2026-01-01'),($9,'provider',$2,'Shoes history','2026-02-01');`, pgx.QueryExecModeSimpleProtocol, buyer, owner, other, provider, first, second, service, oldA, oldB)
	if err != nil {
		t.Fatal(err)
	}
	apply("099_provider_conversation_threads.sql")
	apply("100_notification_sender_identity.sql")
	installPublicIdentityFixture(t, db)
	controller := &ProviderMarketplaceController{db: db}
	receipts := NewMessageReceiptsController(db)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Get("/inbox", controller.ListBuyerConversations)
	community := &CommunityController{db: db, market: controller}
	app.Get("/community-inbox", community.Inbox)
	app.Get("/history/:conversation_id", controller.BuyerConversationMessages)
	app.Post("/product/:product_id", controller.StartProductConversation)
	app.Post("/service/:listing_id", controller.StartProviderConversation)
	app.Post("/read/:conversation_id", controller.BuyerMarkConversationRead)
	app.Post("/receipts", receipts.Acknowledge)
	app.Get("/receipts", receipts.Status)
	call := func(method, path, user, body string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User", user)
		res, e := app.Test(req, 10000)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s %s: status=%d expected=%d", method, path, res.StatusCode, want)
		}
		data := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&data)
		return data
	}
	inbox := call("GET", "/inbox", buyer, "", 200)["items"].([]any)
	if len(inbox) != 1 || inbox[0].(map[string]any)["id"] != oldA {
		t.Fatal("historical products were not grouped", inbox)
	}
	for _, actor := range []string{buyer, owner} {
		rows := call("GET", "/community-inbox?conversation_id="+oldB, actor, "", 200)["items"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["id"] != oldA {
			t.Fatal("unified inbox alias or provider-side grouping failed", rows)
		}
	}
	if rows := call("GET", "/community-inbox?conversation_id="+oldB, other, "", 200)["items"].([]any); len(rows) != 0 {
		t.Fatal("community inbox leaked another customer's thread")
	}
	for _, id := range []string{oldA, oldB} {
		items := call("GET", "/history/"+id+"?mark_read=false&limit=40", buyer, "", 200)["items"].([]any)
		if len(items) != 2 {
			t.Fatal("alias omitted history")
		}
		if items[0].(map[string]any)["context"].(map[string]any)["title"] != "Rice" || items[1].(map[string]any)["context"].(map[string]any)["title"] != "Shoes" {
			t.Fatal("product context lost")
		}
	}
	call("GET", "/history/"+oldB, other, "", 404)
	contact := call("POST", "/service/"+service, buyer, `{"message":"Need delivery","client_message_id":"`+uuid.NewString()+`"}`, 201)
	if contact["id"] != oldA {
		t.Fatal("new service split existing provider chat")
	}
	item := call("GET", "/history/"+oldA+"?mark_read=false", buyer, "", 200)["items"].([]any)[0].(map[string]any)
	call("POST", "/receipts", buyer, `{"channel":"business","conversation_id":"`+oldA+`","message_ids":["`+item["id"].(string)+`"],"read":true}`, 204)
	call("POST", "/read/"+oldA, buyer, `{"message_id":"`+item["id"].(string)+`"}`, 200)
	var marked int
	if e := db.QueryRow(ctx, `SELECT count(*) FROM provider_conversations WHERE provider_id=$1 AND user_id=$2 AND buyer_last_read_at IS NOT NULL`, provider, buyer).Scan(&marked); e != nil || marked != 3 {
		t.Fatal("read watermark not shared", marked, e)
	}
	var sender string
	if e := db.QueryRow(ctx, `SELECT data->>'sender_user_id' FROM notifications WHERE data ? 'provider_event_id' ORDER BY id LIMIT 1`).Scan(&sender); e != nil || sender != buyer {
		t.Fatal("notification sender identity missing", sender, e)
	}
	// Concurrent contacts for two products must acquire one stable inbox identity.
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	failures := make(chan string, 2)
	for _, product := range []string{first, second} {
		wg.Add(1)
		go func(product string) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/product/"+product, strings.NewReader(`{"message":"Concurrent contact","client_message_id":"`+uuid.NewString()+`"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User", other)
			res, e := app.Test(req, 10000)
			if e != nil {
				failures <- e.Error()
				return
			}
			defer res.Body.Close()
			if res.StatusCode != 201 {
				failures <- res.Status
				return
			}
			var data map[string]any
			_ = json.NewDecoder(res.Body).Decode(&data)
			ids <- data["id"].(string)
		}(product)
	}
	wg.Wait()
	close(ids)
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	var root string
	for id := range ids {
		if root != "" && id != root {
			t.Fatal("concurrent products split the thread")
		}
		root = id
	}
	if root == "" {
		t.Fatal("no concurrent contacts completed")
	}
	if _, err = db.Exec(ctx, `UPDATE provider_conversations SET status='blocked' WHERE id=$1::uuid`, oldB); err != nil {
		t.Fatal(err)
	}
	call("POST", "/product/"+first, buyer, `{"message":"Must remain blocked","client_message_id":"`+uuid.NewString()+`"}`, 409)
}
