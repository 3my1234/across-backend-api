package playbilling

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func orderFixture(token, order string) string {
	raw, _ := json.Marshal(map[string]any{"orderId": order, "purchaseToken": token, "state": "PROCESSED", "total": map[string]any{"currencyCode": "NGN", "units": "500", "nanos": 0}, "lineItems": []any{map[string]any{"productId": "atl_basic", "listingPrice": map[string]any{"currencyCode": "NGN", "units": "500", "nanos": 0}, "subscriptionDetails": map[string]any{"basePlanId": "monthly", "offerPhaseDetails": map[string]any{"baseDetails": map[string]any{}}}}}})
	return string(raw)
}
func TestGoogleOrderBindsTokenProductAndActualPaidAmount(t *testing.T) {
	token := "synthetic-order-purchase-token"
	order := "GPA.1111-2222-3333-44444"
	base := orderFixture(token, order)
	for _, test := range []struct {
		raw    string
		ok     bool
		amount int64
	}{
		{base, true, 50000}, {strings.Replace(base, `"total":{"currencyCode":"NGN","nanos":0,"units":"500"}`, `"total":{"currencyCode":"NGN","nanos":0,"units":"537"}`, 1), true, 53700},
		{strings.Replace(base, token, "another-owner-token", 1), false, 0}, {strings.Replace(base, "PROCESSED", "REFUNDED", 1), false, 0}, {strings.Replace(base, "monthly", "different-base", 1), false, 0}, {strings.Replace(base, "500", "499", 1), false, 0},
	} {
		client, _ := New(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			raw := fixture("SUBSCRIPTION_STATE_ACTIVE")
			raw = strings.Replace(raw, `"autoRenewEnabled":true`, `"autoRenewEnabled":true,"recurringPrice":{"currencyCode":"NGN","units":"500","nanos":0}`, 1)
			if strings.Contains(r.URL.Path, "/orders/") {
				raw = test.raw
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
		})}, func(context.Context) (string, error) { return "synthetic-access", nil })
		result, err := client.Verify(context.Background(), token, Expected{ProductID: "atl_basic", BasePlanID: "monthly", AccountID: AccountID("owned-provider"), AmountMinor: 50000, VerifyOrder: true}, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
		if (err == nil) != test.ok || test.ok && result.AmountMinor != test.amount {
			t.Fatalf("Wrong order verification: %+v %v", result, err)
		}
	}
}
