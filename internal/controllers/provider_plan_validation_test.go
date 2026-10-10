package controllers

import (
	"context"
	"io"
	"net/http"
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
