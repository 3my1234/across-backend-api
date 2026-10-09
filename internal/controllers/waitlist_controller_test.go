package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWaitlistConsentDeduplicationPrivacyAndExport(t *testing.T) {
	db := settlementTestDB(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "068_launch_waitlist.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(context.Background(), string(raw)); err != nil {
		t.Fatal(err)
	}
	controller := NewWaitlistController(db)
	app := fiber.New()
	app.Post("/join", controller.Join)
	app.Get("/list", controller.AdminList)
	app.Get("/export", controller.AdminExport)
	send := func(body string, want int) {
		request := httptest.NewRequest("POST", "/join", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		res, e := app.Test(request)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatal(res.StatusCode)
		}
		if want == 200 {
			bytes, _ := io.ReadAll(res.Body)
			if strings.Contains(string(bytes), "@example.test") {
				t.Fatal("public response exposes contact information")
			}
		}
	}
	send(`{"email":"someone@example.test","consent":false}`, 400)
	send(`{"email":"not-an-email","consent":true}`, 400)
	send(`{"email":"bot@example.test","consent":true,"website":"spam"}`, 200)
	send(`{"email":" BUYER@EXAMPLE.TEST ","consent":true,"name":"=IMPORTXML(1)","source":"tiktok","interest":"seller"}`, 200)
	send(`{"email":"buyer@example.test","consent":true,"name":"changed","phone":"wrong"}`, 200)
	res, err := app.Test(httptest.NewRequest("GET", "/list", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var list map[string]any
	if err = json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if list["total"] != float64(1) || list["items"].([]any)[0].(map[string]any)["name"] != "=IMPORTXML(1)" {
		t.Fatal("duplicate overwrote saved consent/profile", list)
	}
	res, err = app.Test(httptest.NewRequest("GET", "/export", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(data), "'=IMPORTXML") || !strings.Contains(string(data), "launch-updates-v1") || res.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("unsafe CSV or missing consent record", string(data))
	}
}

func TestAffordablePlansPreserveExistingCardAgreement(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),code text UNIQUE,name text,description text,amount_ngn numeric,listing_limit int,features jsonb,flutterwave_plan_id bigint,is_active boolean DEFAULT true,updated_at timestamptz DEFAULT now());
 INSERT INTO provider_subscription_plans(code,name,amount_ngn,listing_limit,flutterwave_plan_id) VALUES('provider-monthly','Monthly',500,5,42);`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "066_affordable_provider_plans.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = db.Exec(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var gateway int64
	var basic string
	if err = db.QueryRow(ctx, `SELECT COUNT(*) FROM provider_subscription_plans`).Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	if err = db.QueryRow(ctx, `SELECT name,flutterwave_plan_id FROM provider_subscription_plans WHERE code='provider-monthly'`).Scan(&basic, &gateway); err != nil || basic != "Basic" || gateway != 42 {
		t.Fatal(basic, gateway, err)
	}
	var limits, prices string
	if err = db.QueryRow(ctx, `SELECT string_agg(listing_limit::text,',' ORDER BY amount_ngn),string_agg(amount_ngn::text,',' ORDER BY amount_ngn) FROM provider_subscription_plans`).Scan(&limits, &prices); err != nil || limits != "5,15,40" || prices != "500,1000,2000" {
		t.Fatal(limits, prices, err)
	}
}
