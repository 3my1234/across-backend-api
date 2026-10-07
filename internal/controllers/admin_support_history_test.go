package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestAdminSupportIncludesOlderAnsweredTickets(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,email text);
 CREATE TABLE support_tickets(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid,subject text,message text,status text,created_at timestamptz,updated_at timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO users VALUES($1,'buyer@example.test')`, user)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO support_tickets(user_id,subject,message,status,created_at,updated_at) SELECT $1,'Ticket '||n,'Help',CASE WHEN n<61 THEN 'open' WHEN n<71 THEN 'responded' ELSE 'closed' END,'2026-01-01','2026-01-01' FROM generate_series(1,75)n`, user)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/tickets", NewSupportController(db).AdminListTickets)
	for _, tc := range []struct {
		filter string
		count  int
	}{{"all", 75}, {"responded", 10}, {"closed", 5}, {"open", 60}} {
		cursor := ""
		seen := map[string]bool{}
		for n := 0; n < 5; n++ {
			res, err := app.Test(httptest.NewRequest("GET", "/tickets?limit=25&status="+tc.filter+"&cursor="+cursor, nil))
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Tickets []struct{ ID, Status string }
				Next    string `json:"next_cursor"`
			}
			err = json.NewDecoder(res.Body).Decode(&body)
			res.Body.Close()
			if err != nil || res.StatusCode != 200 {
				t.Fatal(res.StatusCode, err)
			}
			if len(body.Tickets) > 25 {
				t.Fatal("unbounded history")
			}
			for _, ticket := range body.Tickets {
				if seen[ticket.ID] {
					t.Fatal("timestamp tie skipped or duplicated a ticket")
				}
				seen[ticket.ID] = true
				if tc.filter != "all" && ticket.Status != tc.filter {
					t.Fatal("wrong filter")
				}
			}
			cursor = body.Next
			if cursor == "" {
				break
			}
		}
		if len(seen) != tc.count {
			t.Fatal("missing support history", tc.filter, len(seen), tc.count)
		}
	}
	res, err := app.Test(httptest.NewRequest("GET", "/tickets?status=unknown", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("invalid filter accepted")
	}
	res, err = app.Test(httptest.NewRequest("GET", "/tickets?cursor="+encodeAdminCursor(time.Now(), "not-a-uuid"), nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("invalid cursor identity accepted")
	}
}
