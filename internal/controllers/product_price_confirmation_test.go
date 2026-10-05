package controllers

import "testing"

func TestStaleDestinationPriceRequiresExplicitConfirmation(t *testing.T) {
	independent := false
	areas := []productDeliveryArea{{CountryCode: "NG", ItemPrice: 20, DeliveryFee: 5, CurrencyCode: "NGN", UsesPrimaryPrice: &independent}}
	if _, err := normalizeProductDeliveryAreas(areas, "NG", "merchant_local", "NGN", 100); err == nil {
		t.Fatal("stale numeric item price must not silently override edited main price")
	}
	areas[0].IndependentPriceConfirmed = true
	normalized, err := normalizeProductDeliveryAreas(areas, "NG", "merchant_local", "NGN", 100)
	if err != nil || normalized[0].DeliveredPrice != 25 {
		t.Fatalf("confirmed custom price not preserved: %+v %v", normalized, err)
	}
	linked := true
	areas[0].UsesPrimaryPrice = &linked
	areas[0].IndependentPriceConfirmed = false
	normalized, err = normalizeProductDeliveryAreas(areas, "NG", "merchant_local", "NGN", 100)
	if err != nil || normalized[0].DeliveredPrice != 105 {
		t.Fatalf("main price plus delivery not synchronized: %+v %v", normalized, err)
	}
}
