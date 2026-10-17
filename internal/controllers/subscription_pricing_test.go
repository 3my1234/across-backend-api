package controllers

import "testing"

func TestSubscriptionAnnualQuotes(t *testing.T) {
	for _, monthly := range []float64{500, 1000, 2000, 5000} {
		quotes := providerPeriodPrices(monthly, false)
		for _, p := range quotes {
			months := p["duration_months"].(int)
			want := monthly * float64(months)
			if months == 12 {
				want *= 0.9
			}
			if p["amount_ngn"] != want {
				t.Fatalf("price %v months %d: %v", monthly, months, p)
			}
			if months == 12 {
				if p["savings_ngn"] != monthly*12-want || p["discount_percent"] != 10 || p["card_available"] != true || p["card_auto_renews"] != false {
					t.Fatal(p)
				}
			} else if p["savings_ngn"] != float64(0) || p["card_available"] != false {
				t.Fatal(p)
			}
		}
	}
	if providerPeriodPrices(500, true)[0]["card_available"] != true {
		t.Fatal("monthly recurring card disabled")
	}
	total, _ := providerSubscriptionPrice(499.99, 12)
	if total != 5399.89 {
		t.Fatalf("rounding: %v", total)
	}
}
