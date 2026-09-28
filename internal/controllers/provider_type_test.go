package controllers

import "testing"

func TestProviderTypeCapabilities(t *testing.T) {
	tests := []struct {
		providerType string
		products     bool
		services     bool
	}{
		{"product_merchant", true, false},
		{"service_professional", false, true},
		{"property_host", false, true},
		{"mobility_provider", false, true},
		{"mixed", true, true},
		{"other", true, true},
	}
	for _, test := range tests {
		products, services := providerTypeCapabilities(test.providerType)
		if products != test.products || services != test.services {
			t.Fatalf("%s capabilities = (%t,%t), want (%t,%t)", test.providerType, products, services, test.products, test.services)
		}
	}
}

func TestNormalizeProviderTypeRejectsUnknownValue(t *testing.T) {
	if got := normalizeProviderType("crypto_p2p"); got != "" {
		t.Fatalf("normalizeProviderType accepted unregulated provider type %q", got)
	}
	if got := normalizeProviderType(" Property_Host "); got != "property_host" {
		t.Fatalf("normalizeProviderType = %q, want property_host", got)
	}
}
