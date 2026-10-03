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
