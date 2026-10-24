package playbilling

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixture(state string) string {
	return `{"kind":"androidpublisher#subscriptionPurchaseV2","subscriptionState":"` + state + `","acknowledgementState":"ACKNOWLEDGEMENT_STATE_PENDING","externalAccountIdentifiers":{"obfuscatedExternalAccountId":"` + AccountID("owned-provider") + `"},"lineItems":[{"productId":"atl_basic","expiryTime":"2026-11-24T00:00:00Z","latestSuccessfulOrderId":"GPA.1111-2222-3333-44444","autoRenewingPlan":{"autoRenewEnabled":true},"offerDetails":{"basePlanId":"monthly"}}]}`
}

func TestServerVerificationStatesAndAccountBinding(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	expected := Expected{ProductID: "atl_basic", BasePlanID: "monthly", AccountID: AccountID("owned-provider")}
	for _, state := range []string{"ACTIVE", "IN_GRACE_PERIOD", "CANCELED", "ON_HOLD", "PAUSED", "EXPIRED", "PENDING", "PENDING_PURCHASE_CANCELED"} {
		t.Run(state, func(t *testing.T) {
			client, _ := New(&http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Scheme != "https" || req.URL.Host != "androidpublisher.googleapis.com" || !strings.Contains(req.URL.Path, PackageName) || req.Header.Get("Authorization") != "Bearer server-only" {
					t.Fatal("Unverified Google origin or server auth")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture("SUBSCRIPTION_STATE_" + state)))}, nil
			})}, func(context.Context) (string, error) { return "server-only", nil })
			got, err := client.Verify(context.Background(), "synthetic-purchase-token", expected, now)
			if err != nil {
				t.Fatal(err)
			}
			active := state == "ACTIVE" || state == "IN_GRACE_PERIOD" || state == "CANCELED"
			if got.Active != active || got.Pending != (state == "PENDING") {
				t.Fatalf("Wrong entitlement state: %+v", got)
			}
			wrong := expected
			wrong.AccountID = AccountID("another-provider")
			if _, err = client.Verify(context.Background(), "synthetic-purchase-token", wrong, now); err == nil {
				t.Fatal("Cross-account receipt accepted")
			}
		})
	}
}

func TestRejectedReceiptCannotBecomeEntitlementOrExposeTokens(t *testing.T) {
	expected := Expected{ProductID: "atl_basic", BasePlanID: "monthly", AccountID: AccountID("owned-provider")}
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	base := fixture("SUBSCRIPTION_STATE_ACTIVE")
	for _, raw := range []string{`not json`, strings.Replace(base, "atl_basic", "different_sku", 1), strings.Replace(base, "monthly", "different_plan", 1), strings.Replace(base, `"autoRenewEnabled":true`, `"autoRenewEnabled":true,"unrelated":1`, 1)[:20], strings.Replace(base, `"lineItems"`, `"testPurchase":{},"lineItems"`, 1), strings.Replace(base, "SUBSCRIPTION_STATE_ACTIVE", "UNKNOWN", 1), strings.Replace(base, "GPA.1111-2222-3333-44444", "", 1)} {
		client, _ := New(&http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
		})}, func(context.Context) (string, error) { return "server-only", nil })
		got, err := client.Verify(context.Background(), "private-receipt-do-not-log", expected, now)
		if err == nil || got.Active {
			t.Fatal("Invalid receipt accepted")
		}
		if strings.Contains(err.Error(), "private-receipt") || strings.Contains(err.Error(), "server-only") {
			t.Fatal("Credential exposed")
		}
	}
	client, _ := New(&http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("request path contains private-receipt-do-not-log")
	})}, func(context.Context) (string, error) { return "server-only", nil })
	if _, err := client.Verify(context.Background(), "private-receipt-do-not-log", expected, now); err == nil || strings.Contains(err.Error(), "private-receipt") {
		t.Fatal("Transport failure leaked token")
	}
}

func TestExpiryPendingAndRedirectsNeverGrantOrAcknowledge(t *testing.T) {
	expected := Expected{ProductID: "atl_basic", BasePlanID: "monthly", AccountID: AccountID("owned-provider")}
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for _, raw := range []string{
		strings.Replace(fixture("SUBSCRIPTION_STATE_ACTIVE"), "2026-11-24", "2026-09-24", 1),
		fixture("SUBSCRIPTION_STATE_PENDING"),
		fixture("SUBSCRIPTION_STATE_ON_HOLD"),
	} {
		client, _ := New(&http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
		})}, func(context.Context) (string, error) { return "server-only", nil })
		got, err := client.Verify(context.Background(), "synthetic-purchase-token", expected, now)
		if err != nil || got.Active || got.NeedsAcknowledgement {
			t.Fatal("Inactive/pending receipt granted or acknowledged", err)
		}
	}
	calls := 0
	client, _ := New(&http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://untrusted.invalid/"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}, func(context.Context) (string, error) { return "server-only", nil })
	if _, err := client.Verify(context.Background(), "synthetic-purchase-token", expected, now); err == nil || calls != 1 {
		t.Fatal("Verification followed an untrusted redirect")
	}
}
