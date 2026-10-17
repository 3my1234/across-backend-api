package controllers

import "math"

const providerYearlyDiscountPercent = 10

// Applied only when a new checkout is created. Verification uses the immutable
// expected_amount_ngn already saved on that checkout, including older purchases.
func providerSubscriptionPrice(monthly float64, months int) (total, savings float64) {
	base := math.Round(monthly*float64(months)*100) / 100
	total = base
	if months == 12 {
		total = math.Round(base*(100-providerYearlyDiscountPercent)) / 100
	}
	return total, math.Round((base-total)*100) / 100
}

func providerPeriodPrices(monthly float64, monthlyCard bool) []map[string]any {
	prices := make([]map[string]any, 0, 4)
	for _, months := range []int{1, 3, 6, 12} {
		total, savings := providerSubscriptionPrice(monthly, months)
		discount := 0
		if months == 12 {
			discount = providerYearlyDiscountPercent
		}
		prices = append(prices, map[string]any{"duration_months": months, "amount_ngn": total, "savings_ngn": savings, "discount_percent": discount, "card_available": months == 12 || (months == 1 && monthlyCard), "banktransfer_available": true, "card_auto_renews": months == 1})
	}
	return prices
}
