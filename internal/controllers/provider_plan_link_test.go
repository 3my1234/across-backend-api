package controllers

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLinkExistingRecurringPlanWithoutCreatingAnother(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,name text,amount_ngn numeric,flutterwave_plan_id bigint,is_active boolean,updated_at timestamptz);
 CREATE TABLE provider_subscription_plan_price_events(plan_id uuid,admin_id uuid,old_amount_ngn numeric,new_amount_ngn numeric,old_flutterwave_plan_id bigint,new_flutterwave_plan_id bigint)`)
	if err != nil {
		t.Fatal(err)
	}
	plan, admin := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO provider_subscription_plans VALUES($1,'Growth',1000,NULL,true,now())`, plan); err != nil {
		t.Fatal(err)
	}
	amount := "1000"
	calls := 0
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != "GET" || req.URL.Path != "/v3/payment-plans/171539" {
			t.Fatalf("must verify existing plan without creating: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"id":171539,"amount":` + amount + `,"currency":"NGN","interval":"monthly","status":"active"}}`))}, nil
	})}
	controller := &ProviderMarketplaceController{db: db, paymentProvider: &flutterwaveProvider{secretKey: "test", client: client}}
	app := fiber.New()
	app.Post("/plans/:plan_id", func(c *fiber.Ctx) error { c.Locals("admin_id", admin); return controller.AdminChangePlanPrice(c) })
	link := func() int {
		req := httptest.NewRequest("POST", "/plans/"+plan, strings.NewReader(`{"amount_ngn":1000,"flutterwave_plan_id":171539}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if status := link(); status != 200 {
		t.Fatalf("link failed: %d", status)
	}
	var linked int64
	var audit int
	if err = db.QueryRow(ctx, `SELECT flutterwave_plan_id FROM provider_subscription_plans WHERE id=$1`, plan).Scan(&linked); err != nil || linked != 171539 {
		t.Fatalf("linked ID %d %v", linked, err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM provider_subscription_plan_price_events`).Scan(&audit); err != nil || audit != 1 {
		t.Fatalf("audit %d %v", audit, err)
	}
	amount = "2000"
	if status := link(); status != 409 {
		t.Fatalf("mismatched amount accepted: %d", status)
	}
	if calls != 2 {
		t.Fatalf("unexpected remote calls %d", calls)
	}
}
