package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFlutterwaveCreateMonthlyPlanUsesRequestedAmount(t *testing.T) {
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://api.flutterwave.com/v3/payment-plans" {
			t.Fatalf("unexpected request %s %s", req.Method, req.URL)
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["amount"] != float64(500) || body["currency"] != "NGN" || body["interval"] != "monthly" {
			t.Fatalf("unexpected plan request: %+v", body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"id":168976,"amount":500,"currency":"NGN","interval":"monthly","status":"active"}}`))}, nil
	})}
	plan, err := (&flutterwaveProvider{client: client, secretKey: "test"}).CreateMonthlyPaymentPlan(context.Background(), "Provider Monthly", 500)
	if err != nil || plan.ID != 168976 || plan.Amount != 500 {
		t.Fatalf("unexpected new plan %+v, %v", plan, err)
	}
}

func TestFlutterwaveSubscriptionCancellationTargetsExactID(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			if req.Method != http.MethodGet || req.URL.Query().Get("plan") != "168975" || req.URL.Query().Get("status") != "active" {
				t.Fatalf("unexpected list request %s %s", req.Method, req.URL)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","meta":{"page_info":{"total_pages":1}},"data":[{"id":4147,"amount":500,"customer":{"customer_email":"test@example.com"},"plan":168975,"status":"active"}]}`))}, nil
		case 2:
			if req.Method != http.MethodPut || req.URL.String() != "https://api.flutterwave.com/v3/subscriptions/4147/cancel" {
				t.Fatalf("unexpected cancellation request %s %s", req.Method, req.URL)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success"}`))}, nil
		default:
			t.Fatalf("unexpected request %s %s", req.Method, req.URL)
			return nil, nil
		}
	})}
	provider := &flutterwaveProvider{client: client, secretKey: "test"}
	items, err := provider.ListActiveSubscriptions(context.Background(), 168975)
	if err != nil || len(items) != 1 || items[0].Customer.Email != "test@example.com" {
		t.Fatalf("unexpected subscriptions %+v, %v", items, err)
	}
	if err := provider.CancelSubscription(context.Background(), items[0].ID); err != nil {
		t.Fatal(err)
	}
}
