package controllers

import (
	"encoding/json"
	"sort"
	"strings"
)

type listingEdit struct {
	Field  string `json:"field"`
	Label  string `json:"label"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

var listingReviewFields = []string{"listing_type", "title", "description", "category", "contact_email", "contact_phone", "address_line", "city", "state", "country_code", "price", "currency_code", "pricing_unit", "capacity", "media_urls", "attributes", "latitude", "longitude", "service_radius_km", "is_mobile_service", "is_available_now"}
var listingReviewLabels = map[string]string{"listing_type": "Service type", "media_urls": "Photos", "pricing_unit": "Charge per", "service_radius_km": "Travel distance (km)", "is_mobile_service": "Travels to the customer", "is_available_now": "Available now", "contact_email": "Contact email", "contact_phone": "Contact phone", "country_code": "Country", "currency_code": "Currency", "address_line": "Address", "price_mode": "Price type", "price_notes": "Price explanation"}

func listingReviewLabel(field string) string {
	if label := listingReviewLabels[field]; label != "" {
		return label
	}
	return strings.ReplaceAll(field, "_", " ")
}

func listingEdits(baseline, current []byte) []listingEdit {
	edits := make([]listingEdit, 0)
	var before, after map[string]any
	if json.Unmarshal(baseline, &before) != nil || before == nil || json.Unmarshal(current, &after) != nil {
		return edits
	}
	appendChange := func(field, label string, a, b any) {
		first, _ := json.Marshal(a)
		second, _ := json.Marshal(b)
		if string(first) != string(second) {
			edits = append(edits, listingEdit{field, label, a, b})
		}
	}
	for _, field := range listingReviewFields {
		if field == "attributes" {
			a, _ := before[field].(map[string]any)
			b, _ := after[field].(map[string]any)
			keys := map[string]bool{}
			for key := range a {
				keys[key] = true
			}
			for key := range b {
				keys[key] = true
			}
			names := make([]string, 0, len(keys))
			for key := range keys {
				names = append(names, key)
			}
			sort.Strings(names)
			for _, key := range names {
				appendChange("attributes."+key, "Details: "+listingReviewLabel(key), a[key], b[key])
			}
		} else {
			appendChange(field, listingReviewLabel(field), before[field], after[field])
		}
	}
	return edits
}
