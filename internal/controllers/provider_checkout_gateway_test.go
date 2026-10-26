package controllers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCheckoutGatewayCancellationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   int
		body   string
		closed bool
	}{
		{"disabled", 200, `{"status":"success","data":true}`, true},
		{"already disabled", 400, `{"status":"error","message":"This payment link is currently inactive","data":null}`, true},
		{"invalid link", 400, `{"status":"error","message":"Invalid Link","data":null}`, false},
		{"gateway outage", 503, `{"status":"success","data":true}`, false},
		{"incomplete success", 200, `{"status":"success"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			p := &flutterwaveProvider{secretKey: "fixture", client: &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != "POST" || req.URL.String() != "https://api.flutterwave.com/v3/payments/link/disable" || req.Header.Get("Authorization") != "Bearer fixture" {
					t.Fatal("wrong gateway request")
				}
				return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			if e := p.DisableCheckout(context.Background(), "https://checkout.flutterwave.com/v3/hosted/pay/owned"); (e == nil) != tc.closed {
				t.Fatal("incorrect evidence", e)
			}
			for _, link := range []string{"http://checkout.flutterwave.com/pay", "https://attacker.test/pay", "https://checkout.flutterwave.com@attacker.test/pay", "https://checkout.flutterwave.com:444/pay"} {
				if p.DisableCheckout(context.Background(), link) == nil {
					t.Fatal("untrusted link accepted")
				}
			}
			if calls != 1 {
				t.Fatal("untrusted destination called")
			}
		})
	}
	for _, tc := range []struct {
		name    string
		code    int
		body    string
		missing bool
	}{
		{"explicit absent transaction", 400, `{"status":"error","message":"No transaction was found for this id","data":null}`, true},
		{"same text during outage", 503, `{"status":"error","message":"No transaction was found for this id"}`, false},
		{"unknown error", 400, `{"status":"error","message":"Try again"}`, false},
		{"empty successful response", 200, `{"status":"success","data":{}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &flutterwaveProvider{client: &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			_, e := p.VerifyPayment(context.Background(), "", "PROVIDER-owned")
			if e == nil || errors.Is(e, errGatewayPaymentNotFound) != tc.missing {
				t.Fatal("unknown verification unlocked checkout", e)
			}
		})
	}
}
