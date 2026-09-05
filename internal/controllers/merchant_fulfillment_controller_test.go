package controllers

import "testing"

func TestValidFulfillmentTransition(t *testing.T) {
	tests := []struct {
		name, route, owner, from, to string
		want                         bool
	}{
		{"local merchant accepts", "merchant_local", "merchant", "pending", "accepted", true},
		{"local merchant cannot enter international transit", "merchant_local", "merchant", "packed", "international_transit", false},
		{"cross border dispatches", "merchant_cross_border", "merchant", "processing", "dispatched_from_origin", true},
		{"cross border cannot skip to delivered", "merchant_cross_border", "merchant", "accepted", "delivered", false},
		{"merchant hands off to Atlantic", "merchant_cross_border", "merchant", "local_hub", "handed_to_atlantic", true},
		{"Atlantic last mile receives handoff", "merchant_cross_border", "atlantic_last_mile", "handed_to_atlantic", "local_hub", true},
		{"Atlantic cannot change merchant processing", "merchant_cross_border", "atlantic_last_mile", "processing", "dispatched_from_origin", false},
		{"unknown route rejected", "unknown", "merchant", "pending", "accepted", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validFulfillmentTransition(tt.route, tt.owner, tt.from, tt.to); got != tt.want {
				t.Fatalf("validFulfillmentTransition(%q, %q, %q, %q) = %v, want %v", tt.route, tt.owner, tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestLegacyStageForFulfillment(t *testing.T) {
	tests := []struct {
		route, status, stage, orderStatus string
	}{
		{"merchant_local", "accepted", "Order Placed", "Paid"},
		{"merchant_cross_border", "international_transit", "In Transit Internationally", "Shipped"},
		{"merchant_cross_border", "ready_for_pickup", "Arrived at Local Hub", "Shipped"},
		{"merchant_local", "out_for_delivery", "Out for Delivery", "Shipped"},
		{"merchant_local", "delivered", "Delivered", "Delivered"},
	}
	for _, tt := range tests {
		stage, orderStatus := legacyStageForFulfillment(tt.route, tt.status)
		if stage != tt.stage || orderStatus != tt.orderStatus {
			t.Fatalf("legacyStageForFulfillment(%q, %q) = (%q, %q), want (%q, %q)", tt.route, tt.status, stage, orderStatus, tt.stage, tt.orderStatus)
		}
	}
}
