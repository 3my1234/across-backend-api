package controllers

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNormalizeSettlementStatus(t *testing.T) {
	tests := map[string]string{
		"successful": "settled",
		"completed":  "settled",
		"On hold":    "on_hold",
		"flagged":    "on_hold",
		"pending":    "pending",
		"failed":     "failed",
		"reversed":   "reversed",
		"processed":  "pending",
		"on-hold":    "on_hold",
	}
	for input, expected := range tests {
		if actual := normalizeSettlementStatus(input); actual != expected {
			t.Fatalf("normalizeSettlementStatus(%q) = %q; want %q", input, actual, expected)
		}
	}
}

func TestOptionalSettlementAmount(t *testing.T) {
	for _, bad := range []any{"bad", "NaN", math.NaN(), math.Inf(1), -1, "-0.01", 1e12} {
		if _, err := optionalSettlementAmount(bad); err == nil {
			t.Fatalf("accepted invalid amount %v", bad)
		}
	}
	for _, v := range []any{0, json.Number("0"), "0.00"} {
		result, err := optionalSettlementAmount(v, 7)
		if err != nil || result == nil || *result != 0 {
			t.Fatalf("zero replaced by fallback: %v %v", result, err)
		}
	}
	if result, err := optionalSettlementAmount(nil); err != nil || result != nil {
		t.Fatal("missing amount must remain unknown")
	}
	if result, err := optionalSettlementAmount(nil, "19.56"); err != nil || result == nil || *result != 19.56 {
		t.Fatal("valid legacy fallback failed")
	}
}

func TestSettlementHTTPValidatesEnvelopeAndPages(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"valid", `{"status":"success","data":{"id":7016533,"status":"on-hold","currency":"NGN","settlement_account":null,"processed_date":null,"transactions":[{"id":2099189232,"tx_ref":"test","settlement_amount":19.56}]}}`, 200, false},
		{"wrong settlement", `{"status":"success","data":{"id":123}}`, 200, true},
		{"API failure", `{"status":"error","data":{"id":7016533}}`, 200, true},
		{"HTTP failure", `{"status":"success"}`, 403, true},
		{"malformed", `{`, 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Query().Get("page") != "2" || req.Header.Get("Authorization") != "Bearer test-secret" {
					t.Fatal("page or authentication missing")
				}
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			detail, err := getFlutterwaveSettlementPage(context.Background(), "test-secret", "7016533", 2, client)
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if err == nil && gatewayID(detail.Data.Transactions[0].ID) != "2099189232" {
				t.Fatal("numeric ID lost")
			}
		})
	}
}

func TestSettlementListKeepsHistoricalRange(t *testing.T) {
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		query := req.URL.Query()
		if query.Get("page") != "21" || query.Get("from") != "2025-01-01" || query.Get("subaccount_id") != "RS-test" {
			t.Fatal("historical cursor or account filter lost")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","meta":{"page_info":{"total_pages":22}},"data":[{"id":7016533}]}`))}, nil
	})}
	result, err := listFlutterwaveSettlementPage(context.Background(), "test", "RS-test", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Now().UTC(), 21, client)
	if err != nil || len(result.Data) != 1 {
		t.Fatalf("page beyond old cap unavailable: %v", err)
	}
}

func TestSettlementNumericIDsRejectMissingOrFractionalValues(t *testing.T) {
	for _, value := range []any{nil, "", "<nil>", 0, -1, 1.5, "1.5", json.Number("1.5")} {
		if id := settlementNumericID(value); id != "" {
			t.Fatalf("accepted invalid ID %v as %s", value, id)
		}
	}
	if id := settlementNumericID(json.Number("2099189232")); id != "2099189232" {
		t.Fatal("valid transaction ID rejected")
	}
}
