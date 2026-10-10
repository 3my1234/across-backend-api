package controllers

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSupportReplyAndNotificationCommitTogether(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE support_tickets(id uuid PRIMARY KEY,user_id uuid,status text,updated_at timestamptz);
 CREATE TABLE support_messages(ticket_id uuid,sender_type text,sender_id uuid,message text);`)
	if err != nil {
		t.Fatal(err)
	}
	ticket, user, admin := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO support_tickets VALUES($1,$2,'open',now())`, ticket, user); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	controller := NewSupportController(db)
	app.Post("/support/:ticket_id", func(c *fiber.Ctx) error { c.Locals("admin_id", admin); return controller.AdminReply(c) })
	reply := func() int {
		req := httptest.NewRequest("POST", "/support/"+ticket, strings.NewReader(`{"message":"Your account is ready."}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if status := reply(); status != 500 {
		t.Fatalf("failed notification must fail reply: %d", status)
	}
	var count int
	var status string
	if err = db.QueryRow(ctx, `SELECT count(*) FROM support_messages`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial reply persisted: %d %v", count, err)
	}
	if err = db.QueryRow(ctx, `SELECT status FROM support_tickets WHERE id=$1`, ticket).Scan(&status); err != nil || status != "open" {
		t.Fatalf("partial status: %s %v", status, err)
	}
	if _, err = db.Exec(ctx, `CREATE TABLE notifications(user_id uuid,order_id uuid,batch_id uuid,type text,title text,body text,data jsonb,event_key text UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	if status := reply(); status != 200 {
		t.Fatalf("reply status: %d", status)
	}
	var destination, kind string
	if err = db.QueryRow(ctx, `SELECT user_id::text,data->>'ticket_id',type FROM notifications`).Scan(&destination, &status, &kind); err != nil || destination != user || status != ticket || kind != "ticket_reply" {
		t.Fatalf("notification missing routing: %s %s %s %v", destination, status, kind, err)
	}
}
