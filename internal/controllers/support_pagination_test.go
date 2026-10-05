package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestSupportMessagesOwnershipPagingAndTimestampTies(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	user, other, ticket := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `CREATE TABLE support_tickets(id uuid PRIMARY KEY,user_id uuid,status text DEFAULT 'open');
 CREATE TABLE support_messages(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),ticket_id uuid,sender_type text,sender_id text,message text,created_at timestamptz DEFAULT now());`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO support_tickets(id,user_id) VALUES($1,$2);`, ticket, user); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO support_messages(ticket_id,sender_type,sender_id,message) SELECT $1,'user',$2,'message '||n FROM generate_series(1,115) n`, ticket, user); err != nil {
		t.Fatal(err)
	}
	c := NewSupportController(db)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Get("/tickets/:ticket_id/messages", c.GetTicketMessages)
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 3; page++ {
		path := "/tickets/" + ticket + "/messages?limit=50"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-User", user)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Messages []struct {
				ID string `json:"id"`
			}
			Next string `json:"next_cursor"`
		}
		if err = json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal(res.StatusCode)
		}
		if len(body.Messages) > 50 {
			t.Fatal("unbounded page")
		}
		for _, message := range body.Messages {
			if seen[message.ID] {
				t.Fatal("duplicate cursor message")
			}
			seen[message.ID] = true
		}
		cursor = body.Next
	}
	if len(seen) != 115 || cursor != "" {
		t.Fatal(len(seen), cursor)
	}
	for _, tc := range []struct {
		user, query string
		want        int
	}{{other, "", 404}, {user, "?limit=101", 400}, {user, "?cursor=bad", 400}} {
		req := httptest.NewRequest("GET", "/tickets/"+ticket+"/messages"+tc.query, nil)
		req.Header.Set("X-User", tc.user)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Fatal(res.StatusCode, tc.want)
		}
	}
}
