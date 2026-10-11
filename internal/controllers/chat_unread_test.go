package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSupportUnreadAcknowledgementAndNewArrivals(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	user, other, ticket := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,email text); CREATE TABLE support_tickets(id uuid PRIMARY KEY,user_id uuid,subject text,message text,status text,created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now(),user_last_read_at timestamptz,admin_last_read_at timestamptz);
 CREATE TABLE support_messages(id uuid PRIMARY KEY,ticket_id uuid,sender_type text,sender_id text,message text,created_at timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `WITH u AS (INSERT INTO users VALUES($1,'buyer@test')) INSERT INTO support_tickets(id,user_id,subject,message,status) VALUES($2,$1,'Help','Question','open')`, user, ticket); err != nil {
		t.Fatal(err)
	}
	question, answer, newer := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, m := range []struct{ id, sender, stamp string }{{question, "user", "2026-10-11T10:00:00Z"}, {answer, "admin", "2026-10-11T10:01:00Z"}} {
		if _, err = db.Exec(ctx, `INSERT INTO support_messages VALUES($1,$2,$3,$4,'Message',$5)`, m.id, ticket, m.sender, user, m.stamp); err != nil {
			t.Fatal(err)
		}
	}
	sc := NewSupportController(db)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Get("/tickets", sc.ListMyTickets)
	app.Get("/admin", sc.AdminListTickets)
	app.Get("/tickets/:ticket_id/messages", sc.GetTicketMessages)
	app.Post("/tickets/:ticket_id/read", sc.MarkTicketRead)
	app.Post("/admin/:ticket_id/read", sc.AdminMarkTicketRead)
	call := func(method, path, actor, body string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-User", actor)
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s %d expected %d", path, res.StatusCode, want)
		}
		payload := map[string]any{}
		if want == 200 {
			if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
		}
		return payload
	}
	unread := func(path string) int {
		p := call("GET", path, user, "", 200)
		return int(p["tickets"].([]any)[0].(map[string]any)["unread_count"].(float64))
	}
	if unread("/tickets") != 1 || unread("/admin") != 1 {
		t.Fatal("missing unread counters")
	}
	call("GET", "/tickets/"+ticket+"/messages", user, "", 200)
	if unread("/tickets") != 1 {
		t.Fatal("fetch alone must not mark as read")
	}
	if _, err = db.Exec(ctx, `INSERT INTO support_messages VALUES($1,$2,'admin',$3,'Later arrival','2026-10-11T10:02:00Z')`, newer, ticket, user); err != nil {
		t.Fatal(err)
	}
	ack := call("POST", "/tickets/"+ticket+"/read", user, `{"message_id":"`+answer+`"}`, 200)
	if ack["unread_count"] != float64(1) || unread("/tickets") != 1 {
		t.Fatal("later arrival was incorrectly read")
	}
	call("POST", "/tickets/"+ticket+"/read", other, `{"message_id":"`+newer+`"}`, 404)
	call("POST", "/tickets/"+ticket+"/read", user, `{"message_id":"`+newer+`"}`, 200)
	call("POST", "/tickets/"+ticket+"/read", user, `{"message_id":"`+answer+`"}`, 200)
	if unread("/tickets") != 0 {
		t.Fatal("old acknowledgement regressed read position")
	}
	call("POST", "/admin/"+ticket+"/read", "", `{"message_id":"`+question+`"}`, 200)
	if unread("/admin") != 0 {
		t.Fatal("admin read acknowledgement missing")
	}
}

func TestProviderChatReadAcknowledgement(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	buyer, owner, provider, thread := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `CREATE TABLE provider_organizations(id uuid PRIMARY KEY,is_active boolean); CREATE TABLE provider_members(provider_id uuid,user_id uuid,is_active boolean,role text);
 CREATE TABLE provider_conversations(id uuid PRIMARY KEY,user_id uuid,provider_id uuid,buyer_last_read_at timestamptz,provider_last_read_at timestamptz);
 CREATE TABLE provider_conversation_messages(id uuid PRIMARY KEY,conversation_id uuid,sender_type text,body text,created_at timestamptz,media_keys text[] DEFAULT '{}');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `WITH o AS (INSERT INTO provider_organizations VALUES($1,true)), m AS (INSERT INTO provider_members VALUES($1,$2,true,'owner')) INSERT INTO provider_conversations(id,user_id,provider_id) VALUES($3,$4,$1)`, provider, owner, thread, buyer); err != nil {
		t.Fatal(err)
	}
	first, later, question := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, m := range []struct{ id, sender, stamp string }{{first, "provider", "2026-10-11T10:00:00Z"}, {later, "provider", "2026-10-11T10:02:00Z"}, {question, "buyer", "2026-10-11T10:03:00Z"}} {
		if _, err = db.Exec(ctx, `INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,body,created_at) VALUES($1,$2,$3,'Message',$4)`, m.id, thread, m.sender, m.stamp); err != nil {
			t.Fatal(err)
		}
	}
	c := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Post("/buyer/:conversation_id", c.BuyerMarkConversationRead)
	app.Post("/provider/:conversation_id", c.ProviderMarkConversationRead)
	ack := func(path, actor, id string, want int) int {
		t.Helper()
		req := httptest.NewRequest("POST", path+"/"+thread, strings.NewReader(`{"message_id":"`+id+`"}`))
		req.Header.Set("X-User", actor)
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s: %d", path, res.StatusCode)
		}
		var body struct {
			Unread int `json:"unread_count"`
		}
		if want == 200 {
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
		}
		return body.Unread
	}
	if ack("/buyer", buyer, first, 200) != 1 {
		t.Fatal("new arrival incorrectly read")
	}
	ack("/buyer", uuid.NewString(), later, 404)
	if ack("/buyer", buyer, later, 200) != 0 {
		t.Fatal("buyer acknowledgement missing")
	}
	if ack("/provider", owner, question, 200) != 0 {
		t.Fatal("provider acknowledgement missing")
	}
	ack("/provider", buyer, question, 403)
}
