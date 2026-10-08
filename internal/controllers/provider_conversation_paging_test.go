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

func TestProviderChatLatestMessagesAndOlderPages(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	buyer, other, conversation := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `CREATE TABLE provider_conversations(id uuid PRIMARY KEY,user_id uuid,provider_id uuid,buyer_last_read_at timestamptz,provider_last_read_at timestamptz);
 CREATE TABLE provider_conversation_messages(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),conversation_id uuid,sender_type text,body text,created_at timestamptz,media_keys text[] DEFAULT '{}');
 INSERT INTO provider_conversations(id,user_id) VALUES('`+conversation+`','`+buyer+`');`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO provider_conversation_messages(conversation_id,sender_type,body,created_at) SELECT $1,'buyer','message '||n,'2026-10-06'::timestamptz+n*interval '1 second' FROM generate_series(1,115) n`, conversation)
	if err != nil {
		t.Fatal(err)
	}
	controller := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Get("/chat/:conversation_id", func(c *fiber.Ctx) error { return controller.conversationMessages(c, false) })
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 3; page++ {
		req := httptest.NewRequest("GET", "/chat/"+conversation+"?limit=50&cursor="+url.QueryEscape(cursor), nil)
		req.Header.Set("X-User", buyer)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Items []struct {
				ID   string `json:"id"`
				Body string `json:"body"`
			}
			Next string `json:"next_cursor"`
		}
		err = json.NewDecoder(res.Body).Decode(&body)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 {
			t.Fatal(res.StatusCode, err)
		}
		if len(body.Items) > 50 {
			t.Fatal("unbounded chat page")
		}
		if page == 0 && body.Items[len(body.Items)-1].Body != "message 115" {
			t.Fatal("latest reply missing")
		}
		for _, item := range body.Items {
			if seen[item.ID] {
				t.Fatal("duplicate message")
			}
			seen[item.ID] = true
		}
		cursor = body.Next
	}
	if len(seen) != 115 || cursor != "" {
		t.Fatal("history missing", len(seen))
	}
	for _, tc := range []struct {
		user, query string
		status      int
	}{{other, "", 404}, {buyer, "?cursor=bad", 400}, {buyer, "?limit=101", 200}} {
		req := httptest.NewRequest("GET", "/chat/"+conversation+tc.query, nil)
		req.Header.Set("X-User", tc.user)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.status {
			t.Fatal(res.StatusCode, tc.status)
		}
	}
}
