package playbilling

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestUnfulfilledRefundReverifiesOwnershipBeforeMutation(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		posts := 0
		client, _ := New(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			if r.Method == "GET" {
				raw := strings.Replace(fixture("SUBSCRIPTION_STATE_ACTIVE"), "2026-11-24T00:00:00Z", "2099-11-24T00:00:00Z", 1)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
			}
			posts++
			raw, _ := io.ReadAll(r.Body)
			if r.URL.Host != "androidpublisher.googleapis.com" || !strings.HasSuffix(r.URL.Path, ":revoke") || string(raw) != `{"revocationContext":{"fullRefund":{}}}` {
				t.Fatal("Refund did not use fixed Google full-refund endpoint")
			}
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}, func(context.Context) (string, error) { return "synthetic-only", nil })
		expected := Expected{ProductID: "atl_basic", BasePlanID: "monthly", AccountID: AccountID("owned-provider")}
		if wrong {
			expected.AccountID = AccountID("other-provider")
		}
		err := client.RefundUnfulfilled(context.Background(), "synthetic-unfulfilled-token", expected)
		if wrong && (err == nil || posts != 0) || !wrong && (err != nil || posts != 1) {
			t.Fatal("Refund ownership boundary failed", err)
		}
	}
}
