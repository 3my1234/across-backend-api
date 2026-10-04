package controllers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"across/backend/internal/config"

	"github.com/google/uuid"
)

type paymentRoundTripFunc func(*http.Request) (*http.Response, error)

func (f paymentRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMockPaymentsRequireExplicitLocalOptIn(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{"missing key without opt-in", config.Config{AppEnv: "development"}, false},
		{"test key without opt-in", config.Config{AppEnv: "development", FlutterwaveSecretKey: "FLWSECK_TEST_example"}, false},
		{"local opt-in", config.Config{AppEnv: "development", EnableMockPayments: true}, true},
		{"production cannot opt in", config.Config{AppEnv: "production", EnableMockPayments: true}, false},
		{"live key cannot be mocked", config.Config{AppEnv: "development", EnableMockPayments: true, FlutterwaveSecretKey: "FLWSECK-live"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			controller := &PaymentController{cfg: tc.cfg}
			if got := controller.mockPaymentsEnabled(); got != tc.want {
				t.Fatalf("mockPaymentsEnabled()=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestNewPaymentReferenceIsUniqueAndParseable(t *testing.T) {
	orderID := uuid.NewString()
	first := newPaymentReference(orderID)
	second := newPaymentReference(orderID)

	if first == second {
		t.Fatal("payment references must be unique across immediate retries")
	}
	if !strings.HasPrefix(first, "ACROSS-"+orderID+"-") {
		t.Fatalf("unexpected payment reference format: %q", first)
	}
	parsed, err := parseOrderID(first)
	if err != nil {
		t.Fatalf("parseOrderID returned an error: %v", err)
	}
	if parsed != orderID {
		t.Fatalf("parsed order ID %q, want %q", parsed, orderID)
	}
}

func TestValidWebhookSupportsCurrentAndLegacySignatures(t *testing.T) {
	payload := []byte(`{"type":"charge.completed"}`)
	secret := "test-webhook-secret"
	controller := &PaymentController{cfg: config.Config{FlutterwaveWebhookSecret: secret}}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	currentSignature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !controller.validWebhook(payload, currentSignature, "") {
		t.Fatal("current flutterwave-signature should be accepted")
	}
	if !controller.validWebhook(payload, "", secret) {
		t.Fatal("legacy verif-hash should be accepted")
	}
	if controller.validWebhook(payload, "invalid", "") {
		t.Fatal("invalid signature must be rejected")
	}
}

func TestBuildFlutterwaveCustomerIncludesAvailablePhone(t *testing.T) {
	customer := buildFlutterwaveCustomer(" buyer@example.com ", " Buyer Name ", " +2348000000000 ")

	if customer["email"] != "buyer@example.com" {
		t.Fatalf("unexpected customer email: %#v", customer["email"])
	}
	if customer["name"] != "Buyer Name" {
		t.Fatalf("unexpected customer name: %#v", customer["name"])
	}
	if customer["phonenumber"] != "+2348000000000" {
		t.Fatalf("unexpected customer phone: %#v", customer["phonenumber"])
	}
}

func TestBuildFlutterwaveCustomerOmitsMissingPhone(t *testing.T) {
	customer := buildFlutterwaveCustomer("buyer@example.com", "Buyer Name", "  ")
	if _, exists := customer["phonenumber"]; exists {
		t.Fatal("empty customer phone must not be sent to Flutterwave")
	}
}

func TestMissingPurchasingProfileFields(t *testing.T) {
	missing := missingPurchasingProfileFields("buyer@example.com", " ", "")
	if len(missing) != 2 || missing[0] != "full name" || missing[1] != "phone number" {
		t.Fatalf("unexpected missing profile fields: %#v", missing)
	}
	if complete := missingPurchasingProfileFields("buyer@example.com", "Buyer Name", "+2348000000000"); len(complete) != 0 {
		t.Fatalf("complete purchasing profile was rejected: %#v", complete)
	}
}

func TestMarketplaceServiceFeeIsOnePercent(t *testing.T) {
	fee := roundMoney(10000 * marketplaceServiceFeeRate)
	if fee != 100 {
		t.Fatalf("expected a 100 NGN service fee for a 10,000 NGN subtotal, got %.2f", fee)
	}
}

func TestSelectPaymentMethodsUsesCountryPolicy(t *testing.T) {
	methods, err := selectPaymentMethods([]string{"card", "mpesa"}, "mpesa")
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 1 || methods[0] != "mpesa" {
		t.Fatalf("unexpected selected methods: %#v", methods)
	}
	if _, err := selectPaymentMethods([]string{"card", "mpesa"}, "ussd"); err == nil {
		t.Fatal("a payment method outside the country policy must be rejected")
	}
}

func TestFlutterwaveCheckoutUsesImmutableServerValues(t *testing.T) {
	var captured []byte
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		captured, _ = io.ReadAll(req.Body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"status":"success","data":{"link":"https://checkout.example/payment"}}`)),
		}, nil
	})}
	provider := newFlutterwaveProvider("secret", client)
	planID := int64(3807)
	result, err := provider.InitializeCheckout(context.Background(), paymentCheckoutInput{
		Reference: "ACROSS-order-attempt", Amount: 1250.50, Currency: "kes", PaymentPlanID: &planID,
		RedirectURL: "across://payments/flutterwave", PaymentMethods: []string{"card", "mpesa"},
		Customer: map[string]any{"email": "buyer@example.com"},
		Title:    "Checkout", Description: "Test", Metadata: map[string]any{"order_id": "order"},
		Subaccounts: []paymentSubaccount{{ID: "RS_SELLER", TransactionChargeType: "flat", TransactionCharge: 87.25}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(captured)
	for _, required := range []string{
		`"tx_ref":"ACROSS-order-attempt"`,
		`"amount":1250.5`,
		`"currency":"KES"`,
		`"payment_options":"card,mpesa"`,
		`"payment_plan":3807`,
		`"subaccounts":[{"id":"RS_SELLER","transaction_charge_type":"flat","transaction_charge":87.25}]`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("provider payload %s does not contain %s", body, required)
		}
	}
	if result.CheckoutURL != "https://checkout.example/payment" {
		t.Fatalf("unexpected checkout URL: %q", result.CheckoutURL)
	}
}
