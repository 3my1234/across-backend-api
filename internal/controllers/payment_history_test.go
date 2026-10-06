package controllers

import (
	"across/backend/internal/config"
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"testing"
)

func TestBuyerPaymentHistoryOwnershipPaginationAndLifecycle(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `ALTER TABLE orders ADD COLUMN user_id uuid,ADD COLUMN order_status text DEFAULT 'Pending',ADD COLUMN platform_fee numeric DEFAULT 0.10,ADD COLUMN shipping_fee numeric DEFAULT 0,ADD COLUMN xp_discount int DEFAULT 1;
 ALTER TABLE payments ADD COLUMN user_id uuid,ADD COLUMN amount numeric DEFAULT 110.10,ADD COLUMN refund_status text DEFAULT 'none',ADD COLUMN chargeback_status text DEFAULT 'none';`)
	if err != nil {
		t.Fatal(err)
	}
	buyer, other := uuid.NewString(), uuid.NewString()
	for i := 0; i < 38; i++ {
		owner := buyer
		if i >= 31 {
			owner = other
		}
		order := uuid.NewString()
		if _, err = db.Exec(ctx, `INSERT INTO orders(id,user_id) VALUES($1,$2)`, order, owner); err != nil {
			t.Fatal(err)
		}
		paymentOwner := owner
		if i == 37 {
			paymentOwner = buyer
		} // cannot expose an order owned by someone else
		if _, err = db.Exec(ctx, `INSERT INTO payments(order_id,user_id,provider_reference,created_at,payment_status,charged_amount,settlement_status,paid_at) VALUES($1,$2,$3,'2026-10-06T09:00:00Z','processing',112.31,'on_hold',NULL)`, order, paymentOwner, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", buyer); return c.Next() })
	app.Get("/history", NewPaymentController(db, config.Config{}).BuyerPaymentHistory)
	read := func(url string) ([]map[string]any, string, int) {
		t.Helper()
		r, err := app.Test(httptest.NewRequest("GET", url, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			return nil, "", r.StatusCode
		}
		var body struct {
			Payments []map[string]any `json:"payments"`
			Cursor   string           `json:"next_cursor"`
		}
		if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if r.StatusCode == 200 && r.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("private history cached")
		}
		return body.Payments, body.Cursor, r.StatusCode
	}
	first, cursor, status := read("/history?limit=20")
	if status != 200 || len(first) != 20 || cursor == "" {
		t.Fatal(status, len(first), cursor)
	}
	seen := map[string]bool{}
	for _, row := range first {
		seen[row["id"].(string)] = true
		if row["payment_status"] != "processing" || row["seller_settlement_status"] != "on_hold" || row["xp_discount"] != float64(1) || row["service_fee"] != 0.10 || row["amount"] != 110.10 || row["charged_amount"] != 112.31 || row["paid_at"] != nil {
			t.Fatal(row)
		}
	}
	second, next, status := read("/history?limit=20&cursor=" + cursor)
	if status != 200 || len(second) != 11 || next != "" {
		t.Fatal(status, len(second), next)
	}
	for _, row := range second {
		if seen[row["id"].(string)] {
			t.Fatal("duplicate history record")
		}
		seen[row["id"].(string)] = true
	}
	if _, _, status = read("/history?cursor=invalid"); status != 400 {
		t.Fatal(status)
	}
	if _, err = db.Exec(ctx, `UPDATE payments SET payment_status='succeeded',paid_at=now() WHERE id=$1`, first[0]["id"]); err != nil {
		t.Fatal(err)
	}
	fresh, _, status := read("/history?limit=20")
	if status != 200 || fresh[0]["payment_status"] != "succeeded" || fresh[0]["seller_settlement_status"] != "on_hold" {
		t.Fatal("payment confirmation conflated with bank payout", fresh[0])
	}
}
