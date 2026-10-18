package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"testing"
)

func TestNotificationConversationLookupAndProviderReadOwnership(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text,is_active boolean DEFAULT true);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean DEFAULT true);
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,title text,plan_slot integer DEFAULT 1);CREATE TABLE products(id uuid PRIMARY KEY,title text,plan_slot integer DEFAULT 1);
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);
 CREATE TABLE provider_conversations(id uuid PRIMARY KEY,user_id uuid,provider_id uuid,listing_id uuid,product_id uuid,status text DEFAULT 'open',last_message_at timestamptz DEFAULT now(),buyer_last_read_at timestamptz);
 CREATE TABLE provider_conversation_messages(id uuid,conversation_id uuid,sender_type text,body text,created_at timestamptz);
 CREATE TABLE provider_marketplace_events(id uuid PRIMARY KEY,provider_id uuid,read_at timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	provider, seller, buyer, other, listing, old, event := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO provider_organizations(id,business_name) VALUES($1,'Seller')`, []any{provider}},
		{`INSERT INTO provider_members VALUES($1,$2,'owner',true)`, []any{provider, seller}},
		{`INSERT INTO provider_listings VALUES($1,'Service')`, []any{listing}},
		{`INSERT INTO provider_conversations(id,user_id,provider_id,listing_id,last_message_at) VALUES($1,$2,$3,$4,'2000-01-01')`, []any{old, buyer, provider, listing}},
		{`INSERT INTO provider_conversations(id,user_id,provider_id,listing_id) SELECT gen_random_uuid(),$1,$2,$3 FROM generate_series(1,55)`, []any{buyer, provider, listing}},
		{`INSERT INTO provider_marketplace_events(id,provider_id) VALUES($1,$2)`, []any{event, provider}},
	} {
		if _, err = db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	controller := &ProviderMarketplaceController{db: db}
	installPublicIdentityFixture(t, db)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Get("/chats", controller.ListBuyerConversations)
	app.Patch("/events/:notification_id", controller.MarkProviderNotificationRead)
	call := func(method, path, user string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-User", user)
		res, e := app.Test(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatal(res.StatusCode, want)
		}
		var body map[string]any
		if want == 200 {
			if e = json.NewDecoder(res.Body).Decode(&body); e != nil {
				t.Fatal(e)
			}
		}
		return body
	}
	body := call("GET", "/chats?conversation_id="+old+"&limit=1", buyer, 200)
	items := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != old {
		t.Fatal("old thread must be directly reachable", body)
	}
	body = call("GET", "/chats?conversation_id="+old, other, 200)
	if len(body["items"].([]any)) != 0 {
		t.Fatal("other account must not see thread", body)
	}
	call("PATCH", "/events/"+event, other, 403)
	call("PATCH", "/events/"+event, seller, 204)
	call("PATCH", "/events/"+event, seller, 204)
	var read bool
	if err = db.QueryRow(ctx, `SELECT read_at IS NOT NULL FROM provider_marketplace_events WHERE id=$1`, event).Scan(&read); err != nil || !read {
		t.Fatal(err, read)
	}
}
