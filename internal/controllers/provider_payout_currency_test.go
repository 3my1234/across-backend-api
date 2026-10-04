package controllers

import "testing"

func TestPayoutCurrencyForCountry(t *testing.T) {
	for _, country := range []string{"AT", "BE", "BG", "HR", "CY", "EE", "FI", "FR", "DE", "GR", "IE", "IT", "LV", "LT", "LU", "MT", "NL", "PT", "SK", "SI", "ES"} {
		if got := payoutCurrencyForCountry(country); got != "EUR" {
			t.Errorf("%s payout currency = %q, want EUR", country, got)
		}
	}
	for _, tc := range []struct{ country, want string }{
		{"NG", "NGN"}, {"US", "USD"}, {"GB", "GBP"}, {"GH", "GHS"},
		{"PL", ""}, {"DK", ""}, {"CH", ""}, {"XX", ""},
	} {
		if got := payoutCurrencyForCountry(tc.country); got != tc.want {
			t.Errorf("%s payout currency = %q, want %q", tc.country, got, tc.want)
		}
	}
}
