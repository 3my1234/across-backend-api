package controllers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestAdminComplaintCustomerEmail(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,email text UNIQUE);CREATE TABLE complaints(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),order_id uuid,product_id uuid,user_id uuid REFERENCES users(id),admin_id uuid,description text,refund_amount numeric);`); err != nil {
		t.Fatal(err)
	}
	buyer := uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO users VALUES($1,'buyer@example.test')`, buyer); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("admin_id", uuid.NewString()); return c.Next() })
	controller := NewAnalyticsController(db)
	app.Post("/complaints", controller.CreateComplaint)
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"email", `{"description":"Email-linked complaint","user_email":" BUYER@example.test "}`, 201},
		{"missing account", `{"description":"Unknown","user_email":"missing@example.test"}`, 400},
		{"invalid order", `{"description":"Invalid order","order_id":"invalid"}`, 400},
		{"negative amount", `{"description":"Invalid refund","refund_amount":-1}`, 400},
		{"conflicting customer", `{"description":"Conflict","user_email":"buyer@example.test","user_id":"` + uuid.NewString() + `"}`, 400},
		{"no optional email", `{"description":"Unlinked"}`, 201},
		{"legacy customer ID", `{"description":"ID-linked","user_id":"` + buyer + `"}`, 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/complaints", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("status %d want %d", res.StatusCode, tc.want)
			}
		})
	}
	var linked, total int
	if err := db.QueryRow(ctx, `SELECT count(*) FILTER(WHERE user_id=$1),count(*) FROM complaints`, buyer).Scan(&linked, &total); err != nil || linked != 2 || total != 3 {
		t.Fatalf("linked=%d total=%d error=%v", linked, total, err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO users VALUES($1,'BUYER@example.test')`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/complaints", strings.NewReader(`{"description":"Ambiguous","user_email":"BUYER@example.test"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("ambiguous email silently selected a customer")
	}
}
