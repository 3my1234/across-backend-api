package controllers

import (
	"strings"
	"testing"

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
