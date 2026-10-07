package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"testing"
)

func TestBuyerConversationListingLookupIsScoped(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	buyer, other, provider, first, second, conversation := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text);
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,title text);
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);
 CREATE TABLE provider_conversations(id uuid PRIMARY KEY,listing_id uuid,provider_id uuid,user_id uuid,status text DEFAULT 'open',last_message_at timestamptz DEFAULT now(),buyer_last_read_at timestamptz);
 CREATE TABLE provider_conversation_messages(id uuid PRIMARY KEY,conversation_id uuid,sender_type text,body text,created_at timestamptz DEFAULT now());`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO provider_organizations VALUES($1,'Repair provider')`, provider)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO provider_listings VALUES($1,$3,'First service'),($2,$3,'Second service')`, first, second, provider)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO provider_conversations(id,listing_id,provider_id,user_id) VALUES($1,$2,$3,$4),($5,$6,$3,$4)`, conversation, first, provider, buyer, uuid.NewString(), second)
	if err != nil {
		t.Fatal(err)
	}
	controller := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Get("/chats", controller.ListBuyerConversations)
	for _, tc := range []struct {
		user, listing string
		count, status int
	}{{buyer, first, 1, 200}, {buyer, "", 2, 200}, {other, first, 0, 200}, {buyer, "bad", 0, 400}} {
		req := httptest.NewRequest("GET", "/chats?listing_id="+tc.listing, nil)
		req.Header.Set("X-User", tc.user)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != tc.status {
			res.Body.Close()
			t.Fatal(res.StatusCode, tc.status)
		}
		if tc.status == 200 {
			var body struct {
				Items []struct {
					ID        string
					ListingID string `json:"listing_id"`
				}
			}
			if err = json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Items) != tc.count {
				t.Fatal("lookup leaked or omitted conversations", len(body.Items))
			}
			if tc.listing != "" && tc.count > 0 && body.Items[0].ID != conversation {
				t.Fatal("wrong conversation returned")
			}
		}
		res.Body.Close()
	}
}
