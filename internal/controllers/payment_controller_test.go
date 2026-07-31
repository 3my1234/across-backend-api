package controllers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"across/backend/internal/config"

	"github.com/google/uuid"
)

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
