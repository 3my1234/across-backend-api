package controllers

import "testing"

func TestServicePriceModes(t *testing.T) {
	price, lat, lon := 1000.0, 9.0, 7.0
	base := listingPayload{ListingType: "mechanic", Title: "Repair", Description: "Repair service", City: "Abuja", Latitude: &lat, Longitude: &lon, MediaURLs: []string{"https://example.test/image.jpg"}}
	for _, tc := range []struct {
		mode      any
		price     *float64
		wantError bool
	}{
		{"fixed", &price, false}, {"from", &price, false}, {"quote", nil, false}, {"fixed", nil, true}, {"from", nil, true}, {"quote", &price, true}, {"unknown", nil, true}, {map[string]any{"bad": true}, nil, true},
	} {
		req := base
		req.Price = tc.price
		req.Attributes = map[string]any{"price_mode": tc.mode}
		if err := validateListing(req); (err != nil) != tc.wantError {
			t.Errorf("mode %v: %v", tc.mode, err)
		}
	}
	base.Attributes = map[string]any{"price_notes": 123}
	if validateListing(base) == nil {
		t.Fatal("non-text price notes accepted")
	}
}
