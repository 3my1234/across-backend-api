package playbilling

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAcknowledgementRequiresCurrentVerifiedOwnershipAndState(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	expected := Expected{ProductID: "atl_basic", BasePlanID: "monthly", AccountID: AccountID("owned-provider")}
	for _, test := range []struct {
		state string
		wrong bool
		posts int
		ok    bool
	}{
		{"ACTIVE", false, 1, true}, {"PENDING", false, 0, false}, {"ON_HOLD", false, 0, false}, {"ACTIVE", true, 0, false},
	} {
		t.Run(test.state+map[bool]string{true: "wrong", false: "owned"}[test.wrong], func(t *testing.T) {
			posts := 0
			client, _ := New(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture("SUBSCRIPTION_STATE_" + test.state)))}, nil
				}
				posts++
				if r.URL.Host != "androidpublisher.googleapis.com" || !strings.HasSuffix(r.URL.Path, ":acknowledge") || r.Header.Get("Authorization") != "Bearer server-only" {
					t.Fatal("Invalid acknowledgement origin/authentication")
				}
				return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
			})}, func(context.Context) (string, error) { return "server-only", nil })
			want := expected
			if test.wrong {
				want.AccountID = AccountID("someone-else")
			}
			receipt, err := client.Acknowledge(context.Background(), "synthetic-purchase-token", want, now)
			if (err == nil) != test.ok || posts != test.posts || test.ok && receipt.NeedsAcknowledgement {
				t.Fatalf("Unexpected acknowledgement result posts=%d error=%v", posts, err)
			}
		})
	}
}
func TestAcknowledgedReceiptSkipsMutationAndFailedMutationStaysPending(t *testing.T) {
	for _, already := range []bool{false, true} {
		posts := 0
		client, _ := New(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				raw := fixture("SUBSCRIPTION_STATE_ACTIVE")
				if already {
					raw = strings.Replace(raw, "ACKNOWLEDGEMENT_STATE_PENDING", "ACKNOWLEDGEMENT_STATE_ACKNOWLEDGED", 1)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
			}
			posts++
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("private-receipt"))}, nil
		})}, func(context.Context) (string, error) { return "server-only", nil })
		receipt, err := client.Acknowledge(context.Background(), "synthetic-purchase-token", Expected{ProductID: "atl_basic", BasePlanID: "monthly", AccountID: AccountID("owned-provider")}, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
		if already {
			if err != nil || posts != 0 {
				t.Fatal("Duplicate acknowledgement mutated Google")
			}
		} else if err == nil || !receipt.NeedsAcknowledgement || strings.Contains(err.Error(), "private-receipt") {
			t.Fatal("Uncertain mutation was labelled confirmed or leaked response")
		}
	}
}
func TestVaultAuthenticatesProviderAndRejectsTampering(t *testing.T) {
	v, err := NewVault(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := v.Seal("provider-a", "sensitive-purchase-token")
	second, _ := v.Seal("provider-a", "sensitive-purchase-token")
	if string(first) == string(second) || strings.Contains(string(first), "sensitive") {
		t.Fatal("Token encryption reused nonce or exposed plaintext")
	}
	if got, err := v.Open("provider-a", first); err != nil || got != "sensitive-purchase-token" {
		t.Fatal("Stored token cannot be reconciled")
	}
	if _, err := v.Open("provider-b", first); err == nil {
		t.Fatal("Cross-provider token decrypted")
	}
	first[len(first)-1] ^= 1
	if _, err := v.Open("provider-a", first); err == nil {
		t.Fatal("Tampered token accepted")
	}
	if _, err := NewVault(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Fatal("Short token key accepted")
	}
}
