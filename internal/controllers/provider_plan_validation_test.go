package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestProviderCheckoutRejectsMismatchedGatewayPlan(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount string
		status string
		want   int
	}{
		{name: "matching plan", amount: "100", status: "active", want: 0},
		{name: "different recurring amount", amount: "500", status: "active", want: fiber.StatusConflict},
		{name: "inactive gateway plan", amount: "100", status: "cancelled", want: fiber.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.String() != "https://api.flutterwave.com/v3/payment-plans/168975" {
					t.Fatalf("unexpected gateway request: %s %s", req.Method, req.URL)
				}
				body := `{"status":"success","data":{"id":168975,"amount":` + tc.amount + `,"currency":"NGN","interval":"monthly","status":"` + tc.status + `"}}`
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			controller := &ProviderMarketplaceController{paymentProvider: &flutterwaveProvider{secretKey: "test", client: client}}
			err := controller.validateMonthlyPaymentPlan(context.Background(), 168975, 100)
			if tc.want == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			fiberErr, ok := err.(*fiber.Error)
			if !ok || fiberErr.Code != tc.want {
				t.Fatalf("expected HTTP %d; got %v", tc.want, err)
			}
		})
	}
}

func TestPaidAccessAllowsTransferOnlyTiersAndValidatesLinkedCardPlans(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_subscription_plans(amount_ngn numeric,flutterwave_plan_id bigint,is_active boolean);
	 INSERT INTO provider_subscription_plans VALUES(500,168975,true),(1000,NULL,true),(2000,NULL,true)`)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path != "/v3/payment-plans/168975" {
			t.Fatalf("unexpected plan: %s", req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"id":168975,"amount":500,"currency":"NGN","interval":"monthly","status":"active"}}`)), Header: make(http.Header)}, nil
	})}
	controller := &ProviderMarketplaceController{db: db, paymentProvider: &flutterwaveProvider{secretKey: "test", client: client}}
	if err := controller.validateActiveProviderPlans(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected only the linked card tier to be checked, got %d", calls)
	}
	if _, err := db.Exec(ctx, `UPDATE provider_subscription_plans SET amount_ngn=600 WHERE flutterwave_plan_id=168975`); err != nil {
		t.Fatal(err)
	}
	if err := controller.validateActiveProviderPlans(ctx); err == nil {
		t.Fatal("a mismatched linked recurring tier must still block paid access")
	}
	if _, err := db.Exec(ctx, `UPDATE provider_subscription_plans SET is_active=false`); err != nil {
		t.Fatal(err)
	}
	if err := controller.validateActiveProviderPlans(ctx); err == nil {
		t.Fatal("paid access requires at least one active tier")
	}
}

func TestAdminPlanVerificationHandlesUnlinkedTiers(t *testing.T) {
	db := settlementTestDB(t)
	_, err := db.Exec(context.Background(), `CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),code text,name text,description text,amount_ngn numeric,billing_interval text,listing_limit int,flutterwave_plan_id bigint,is_active boolean,created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now());
	 INSERT INTO provider_subscription_plans(code,name,description,amount_ngn,billing_interval,listing_limit,flutterwave_plan_id,is_active) VALUES
	 ('provider-monthly','Basic','Basic',500,'monthly',5,168975,true),
	 ('provider-growth','Growth','Growth',1000,'monthly',15,NULL,true),
	 ('provider-business','Business','Business',2000,'monthly',40,NULL,true)`)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path != "/v3/payment-plans/168975" {
			t.Fatalf("unlinked tier requested gateway verification: %s", req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"id":168975,"amount":500,"currency":"NGN","interval":"monthly","status":"active"}}`))}, nil
	})}
	controller := &ProviderMarketplaceController{db: db, paymentProvider: &flutterwaveProvider{secretKey: "test", client: client}}
	app := fiber.New()
	app.Get("/plans", controller.AdminListPlans)
	for _, path := range []string{"/plans", "/plans?verify=true"} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Items []struct {
				Name    string `json:"name"`
				ID      *int64 `json:"flutterwave_plan_id"`
				Matches bool   `json:"gateway_price_matches"`
			} `json:"items"`
		}
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || len(payload.Items) != 3 {
			t.Fatalf("plan listing failed: %s %d %v", path, resp.StatusCode, err)
		}
		if payload.Items[1].ID != nil || payload.Items[2].ID != nil {
			t.Fatal("unlinked tiers must remain available without a gateway ID")
		}
		if path == "/plans?verify=true" && !payload.Items[0].Matches {
			t.Fatal("linked Basic plan was not verified")
		}
	}
	if calls != 1 {
		t.Fatalf("gateway calls=%d, want 1", calls)
	}
}
