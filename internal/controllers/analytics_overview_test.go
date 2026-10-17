package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestAdminAnalyticsOverview(t *testing.T) {
	db := settlementTestDB(t)
	_, err := db.Exec(context.Background(), `ALTER TABLE payments ADD COLUMN amount numeric, ADD COLUMN refund_status text DEFAULT 'none', ADD COLUMN chargeback_status text DEFAULT 'none';
 CREATE TABLE complaints(created_at timestamptz,status text,refund_amount numeric);
 INSERT INTO payments(purpose,payment_status,currency_code,paid_at,amount) VALUES
 ('provider_subscription','succeeded','NGN',now()-interval '1 minute',500),
 ('provider_subscription','succeeded','NGN',now()-interval '1 minute',1000),
 ('provider_subscription','failed','NGN',now(),5000),
 ('provider_subscription','succeeded','USD',now(),100),
 ('order','succeeded','NGN',now(),9000),
 ('provider_subscription','succeeded','NGN',now()-interval '31 days',10000),
 ('provider_subscription','succeeded','NGN',now()+interval '1 day',10000);
 UPDATE payments SET refund_status='partial' WHERE amount=1000;
 INSERT INTO complaints VALUES(now(),'resolved',200),(now(),'unresolved',100),(now()-interval '40 days','resolved',10000);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"super_admin", "catalog_admin"} {
		app := fiber.New()
		app.Use(func(c *fiber.Ctx) error { c.Locals("admin_role", role); return c.Next() })
		app.Get("/", NewAnalyticsController(db).GetOverview)
		res, err := app.Test(httptest.NewRequest("GET", "/", nil))
		if err != nil {
			t.Fatal(err)
		}
		var data struct {
			Receipts   float64  `json:"receipts"`
			Gross      *float64 `json:"gross_receipts"`
			Refund     *float64 `json:"recorded_refunds"`
			Complaints int      `json:"complaints"`
			Unresolved int      `json:"unresolved_complaints"`
			Flagged    int      `json:"flagged_receipts"`
			Daily      []struct {
				Receipts float64  `json:"receipts"`
				Gross    *float64 `json:"gross_receipts"`
			} `json:"daily"`
		}
		err = json.NewDecoder(res.Body).Decode(&data)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("status=%d error=%v", res.StatusCode, err)
		}
		if data.Receipts != 2 || data.Complaints != 2 || data.Unresolved != 1 || data.Flagged != 1 || len(data.Daily) != 30 {
			t.Fatalf("wrong aggregation: %+v", data)
		}
		if role == "super_admin" {
			if data.Gross == nil || *data.Gross != 1500 || data.Refund == nil || *data.Refund != 200 {
				t.Fatalf("incorrect gross/refund: %+v", data)
			}
		} else {
			if data.Gross != nil || data.Refund != nil {
				t.Fatal("catalog admin received money")
			}
			for _, d := range data.Daily {
				if d.Gross != nil {
					t.Fatal("daily money leaked")
				}
			}
		}
		total := float64(0)
		for _, d := range data.Daily {
			total += d.Receipts
		}
		if total != data.Receipts {
			t.Fatal("daily rows disagree with total")
		}
	}
}
