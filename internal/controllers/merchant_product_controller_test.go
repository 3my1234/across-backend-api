package controllers

import "testing"

func validMerchantProductPayload() merchantProductPayload {
	latitude, longitude := 9.0765, 7.3986

	return merchantProductPayload{
		Title:                "Palm oil",
		Description:          "Locally stocked palm oil",
		ImageURLs:            []string{"https://media.atlxpres.com/products/palm-oil.jpg"},
		Price:                50000,
		InventoryCount:       100,
		FulfillmentMode:      "merchant_local",
		InventoryCountryCode: "NG",
		InventoryCity:        "Abuja",
		InventoryLocation:    "Banex, Wuse 2",
		StockState:           "locally_available",
		InventoryLatitude:    &latitude,
		InventoryLongitude:   &longitude,
		HandlingTimeHours:    24,
		DeliveryMinDays:      1,
		DeliveryMaxDays:      3,
		DeliveryMethods:      []string{"delivery", "pickup"},
	}
}

func TestNormalizeMerchantProductAcceptsLegacyLocalPortalValues(t *testing.T) {
	req := validMerchantProductPayload()
	req.InventoryCountryCode = "ng"
	req.StockState = "in_stock"

	normalizeMerchantProduct(&req)

	if req.InventoryCountryCode != "NG" {
		t.Fatalf("expected normalized country NG, got %q", req.InventoryCountryCode)
	}
	if req.StockState != "locally_available" {
		t.Fatalf("expected canonical local stock state, got %q", req.StockState)
	}
	if err := validateMerchantProduct(req); err != nil {
		t.Fatalf("expected normalized legacy payload to validate, got %v", err)
	}
}

func TestNormalizeMerchantProductAcceptsLegacyCrossBorderPortalValues(t *testing.T) {
	req := validMerchantProductPayload()
	req.FulfillmentMode = "merchant_cross_border"
	req.InventoryCountryCode = "cn"
	req.StockState = "preorder"

	normalizeMerchantProduct(&req)

	if req.InventoryCountryCode != "CN" {
		t.Fatalf("expected normalized country CN, got %q", req.InventoryCountryCode)
	}
	if req.StockState != "import_on_demand" {
		t.Fatalf("expected canonical cross-border stock state, got %q", req.StockState)
	}
	if err := validateMerchantProduct(req); err != nil {
		t.Fatalf("expected normalized legacy payload to validate, got %v", err)
	}
}

func TestValidateMerchantProductRejectsCrossBorderStockInNigeria(t *testing.T) {
	req := validMerchantProductPayload()
	req.FulfillmentMode = "merchant_cross_border"
	req.StockState = "foreign_stock"

	if err := validateMerchantProduct(req); err == nil {
		t.Fatal("expected cross-border inventory in Nigeria to be rejected")
	}
}

func TestValidateMerchantProductRequiresLocalCoordinates(t *testing.T) {
	req := validMerchantProductPayload()
	req.InventoryLatitude = nil
	req.InventoryLongitude = nil

	if err := validateMerchantProduct(req); err == nil {
		t.Fatal("expected local stock without coordinates to be rejected")
	}
}
