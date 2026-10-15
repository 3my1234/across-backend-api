package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCommunityMessageReceiptsPrivacyAndAuthorization(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE admins(id uuid PRIMARY KEY);CREATE TABLE support_tickets(id uuid PRIMARY KEY,user_id uuid);CREATE TABLE support_messages(id uuid PRIMARY KEY,ticket_id uuid,sender_type text,sender_id uuid)`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/091_message_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	buyer, provider, outsider, org, room, message, ticket, admin := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, actor := range []string{buyer, provider, outsider} {
		if _, err = db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Test')`, actor); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner')`, org, provider); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_conversations(id,provider_id,user_id,status) VALUES($1,$2,$3,'open')`, room, org, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `ALTER TABLE provider_conversation_messages ADD COLUMN sender_user_id uuid; INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,sender_user_id) VALUES('`+message+`','`+room+`','buyer','`+buyer+`')`); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if c.Get("Admin") != "" {
			c.Locals("admin_id", c.Get("Admin"))
		} else {
			c.Locals("user_id", c.Get("Actor"))
		}
		return c.Next()
	})
	controller := NewMessageReceiptsController(db)
	app.Get("/preferences", controller.Preferences)
	app.Patch("/preferences", controller.SavePreferences)
	app.Get("/receipts", controller.Status)
	app.Post("/receipts", controller.Acknowledge)
	app.Post("/deliveries", controller.BatchDelivered)
	call := func(method, path, actor, body string, asAdmin bool, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if asAdmin {
			req.Header.Set("Admin", actor)
		} else {
			req.Header.Set("Actor", actor)
		}
		res, e := app.Test(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var data map[string]any
		_ = json.NewDecoder(res.Body).Decode(&data)
		if res.StatusCode != want {
			t.Fatalf("%s %s status%d want%d: %v", method, path, res.StatusCode, want, data)
		}
		return data
	}
	ack := func(channel, conversation, id, who string, read, asAdmin bool) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"channel": channel, "conversation_id": conversation, "message_ids": []string{id}, "read": read})
		call("POST", "/receipts", who, string(payload), asAdmin, 204)
	}
	status := func(channel, conversation, id, who string, asAdmin bool) map[string]any {
		t.Helper()
		data := call("GET", "/receipts?channel="+channel+"&conversation_id="+conversation+"&message_ids="+id, who, "", asAdmin, 200)
		return data["items"].(map[string]any)[id].(map[string]any)
	}
	assertStatus := func(want string) {
		t.Helper()
		if got := status("business", room, message, buyer, false)["status"]; got != want {
			t.Fatalf("got%v want%s", got, want)
		}
	}
	assertStatus("sent")
	ack("business", room, message, buyer, true, false)
	assertStatus("sent") // sender cannot acknowledge itself
	batch := `{"messages":[{"channel":"business","conversation_id":"` + room + `","message_id":"` + message + `"}],"read":true,"recipient_id":"` + outsider + `"}`
	call("POST", "/deliveries", outsider, batch, false, 204)
	assertStatus("sent")
	call("POST", "/deliveries", provider, batch, false, 204)
	assertStatus("delivered") // batch never publishes a read
	call("POST", "/deliveries", provider, batch, false, 204)
	ack("business", room, message, provider, false, false)
	assertStatus("delivered")
	call("PATCH", "/preferences", provider, `{"read_receipts_enabled":false}`, false, 200)
	ack("business", room, message, provider, true, false)
	assertStatus("delivered")
	call("PATCH", "/preferences", provider, `{"read_receipts_enabled":true}`, false, 200)
	assertStatus("delivered") // no retrospective disclosure
	ack("business", room, message, provider, true, false)
	assertStatus("read")
	call("PATCH", "/preferences", buyer, `{"read_receipts_enabled":false}`, false, 200)
	assertStatus("delivered")
	call("PATCH", "/preferences", buyer, `{"read_receipts_enabled":true}`, false, 200)
	assertStatus("read")
	call("GET", "/receipts?channel=business&conversation_id="+room+"&message_ids="+message, outsider, "", false, 403)
	invalid, _ := json.Marshal(map[string]any{"channel": "business", "conversation_id": room, "message_ids": []string{message}, "read": true})
	call("POST", "/receipts", outsider, string(invalid), false, 403)
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM message_receipts`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate acknowledgements", count, err)
	}
	// Support works across distinct admin and user identities with reciprocal privacy.
	supportMessage := uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO support_tickets(id,user_id)VALUES($1,$2)`, ticket, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO support_messages VALUES($1,$2,'user',$3)`, supportMessage, ticket, buyer); err != nil {
		t.Fatal(err)
	}
	ack("support", ticket, supportMessage, admin, true, true)
	if status("support", ticket, supportMessage, buyer, false)["status"] != "read" {
		t.Fatal("admin read missing")
	}
	call("PATCH", "/preferences", admin, `{"read_receipts_enabled":false}`, true, 200)
	if status("support", ticket, supportMessage, buyer, false)["status"] != "delivered" {
		t.Fatal("admin preference leaked read")
	}
	// Shared rooms reject outsiders and removed participants.
	shared, sharedMessage := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO chat_rooms(id,kind,owner_user_id,direct_low,direct_high) VALUES($1,'direct',$2,LEAST($2::uuid,$3::uuid),GREATEST($2::uuid,$3::uuid))`, shared, buyer, provider); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,role) VALUES($1,$2,'member'),($1,$3,'member')`, shared, buyer, provider); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO chat_messages(id,room_id,sender_user_id,body,client_message_id) VALUES($1,$2,$3,'Hello',$4)`, sharedMessage, shared, buyer, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	ack("shared", shared, sharedMessage, provider, true, false)
	if status("shared", shared, sharedMessage, buyer, false)["read_count"] != float64(1) {
		t.Fatal("shared read missing")
	}
	if _, err = db.Exec(ctx, `DELETE FROM chat_participants WHERE room_id=$1 AND user_id=$2`, shared, provider); err != nil {
		t.Fatal(err)
	}
	invalid, _ = json.Marshal(map[string]any{"channel": "shared", "conversation_id": shared, "message_ids": []string{sharedMessage}})
	call("POST", "/receipts", provider, string(invalid), false, 403)
	call("POST", "/receipts", buyer, `{"channel":"business","conversation_id":"`+room+`","message_ids":[]}`, false, 422)
	if _, err = db.Exec(ctx, `DELETE FROM provider_conversation_messages WHERE id=$1`, message); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM message_receipts WHERE channel='business'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("orphan receipts", count, err)
	}
	// Account removal clears direct rooms with RESTRICT references first.
	if _, err = db.Exec(ctx, `DELETE FROM chat_rooms WHERE id=$1`, shared); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO message_receipts(channel,conversation_id,message_id,recipient_type,recipient_id) VALUES('support',$1,$2,'user',$3)`, ticket, uuid.NewString(), provider); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `DELETE FROM users WHERE id=$1`, provider); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM message_receipts WHERE recipient_type='user' AND recipient_id=$1)+(SELECT count(*) FROM messaging_preferences WHERE actor_type='user' AND actor_id=$1)`, provider).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted user receipt metadata retained", count, err)
	}
}
