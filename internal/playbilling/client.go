// Package playbilling verifies Google-origin subscription state. It does not
// accept client-claimed price, expiry or ownership and never grants DB access.
package playbilling

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const PackageName = "com.atlanticexpress.mobile"

var skuPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,199}$`)
var accountPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// AccessToken must come from a server-owned Android Publisher credential source.
// Service-account credentials and OAuth tokens must never come from a request.
type AccessToken func(context.Context) (string, error)

type Client struct {
	http  *http.Client
	token AccessToken
}

func New(client *http.Client, token AccessToken) (*Client, error) {
	if token == nil {
		return nil, errors.New("Play credential source is required")
	}
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	copy.Timeout = 10 * time.Second
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &copy, token: token}, nil
}

// AccountID is stable across sign-in methods/workspace switches. Call only
// after resolving an authenticated user's permitted provider on the backend.
func AccountID(providerID string) string {
	digest := sha256.Sum256([]byte("atl-provider-v1:" + providerID))
	return hex.EncodeToString(digest[:])
}

// Expected is a server-owned product mapping. A client cannot choose the
// entitlement tier, Google account binding or whether test purchases are allowed.
type Expected struct {
	ProductID, BasePlanID, AccountID string
	AllowTest                        bool
	// Nonzero requires Google's undiscounted NGN recurring price to match the
	// server catalog exactly. The ledger never accepts mobile price claims.
	AmountMinor int64
	Prepaid     bool
	VerifyOrder bool
}

type Verified struct {
	ProductID, BasePlanID, OrderID, State                   string
	ExpiresAt                                               time.Time
	Active, Pending, AutoRenews, Test, NeedsAcknowledgement bool
	AmountMinor                                             int64
}

type purchase struct {
	Kind                 string    `json:"kind"`
	State                string    `json:"subscriptionState"`
	AcknowledgementState string    `json:"acknowledgementState"`
	Test                 *struct{} `json:"testPurchase"`
	Account              struct {
		ID string `json:"obfuscatedExternalAccountId"`
	} `json:"externalAccountIdentifiers"`
	Items []struct {
		ProductID    string `json:"productId"`
		Expiry       string `json:"expiryTime"`
		OrderID      string `json:"latestSuccessfulOrderId"`
		AutoRenewing *struct {
			Enabled bool `json:"autoRenewEnabled"`
			Price   struct {
				Currency string `json:"currencyCode"`
				Units    string `json:"units"`
				Nanos    int64  `json:"nanos"`
			} `json:"recurringPrice"`
		} `json:"autoRenewingPlan"`
		Prepaid *struct{} `json:"prepaidPlan"`
		Offer   struct {
			BasePlanID string `json:"basePlanId"`
			OfferID    string `json:"offerId"`
		} `json:"offerDetails"`
		Phase struct {
			FreeTrial    *struct{} `json:"freeTrial"`
			Introductory *struct{} `json:"introductoryPrice"`
		} `json:"offerPhase"`
	} `json:"lineItems"`
}

// Verify performs a read only on Google's fixed TLS endpoint. The caller must
// commit a token-unique, account-bound ledger transaction before acknowledgement
// or a grant, and must reconcile later cancellations/refunds independently.
func (c *Client) Verify(ctx context.Context, purchaseToken string, expected Expected, now time.Time) (Verified, error) {
	if len(purchaseToken) < 16 || len(purchaseToken) > 4096 || strings.TrimSpace(purchaseToken) != purchaseToken || strings.ContainsAny(purchaseToken, "\r\n\x00") || !skuPattern.MatchString(expected.ProductID) || !skuPattern.MatchString(expected.BasePlanID) || !accountPattern.MatchString(expected.AccountID) || now.IsZero() {
		return Verified{}, errors.New("Invalid Play verification input")
	}
	access, err := c.token(ctx)
	if err != nil || access == "" || strings.ContainsAny(access, "\r\n") {
		return Verified{}, errors.New("Play authentication is unavailable")
	}
	endpoint := "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/" + PackageName + "/purchases/subscriptionsv2/tokens/" + url.PathEscape(purchaseToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Verified{}, errors.New("Play verification request is unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+access)
	response, err := c.http.Do(req)
	if err != nil {
		return Verified{}, errors.New("Play verification transport is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Verified{}, errors.New("Play has not confirmed this subscription")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return Verified{}, errors.New("Play response is invalid")
	}
	var receipt purchase
	if json.Unmarshal(data, &receipt) != nil {
		return Verified{}, errors.New("Play response is invalid")
	}
	verified, err := validate(receipt, expected, now)
	if err == nil && verified.Active && expected.VerifyOrder {
		verified.AmountMinor, err = c.verifyOrder(ctx, purchaseToken, verified, expected)
	}
	return verified, err
}

func validate(receipt purchase, expected Expected, now time.Time) (Verified, error) {
	if receipt.Kind != "androidpublisher#subscriptionPurchaseV2" || len(receipt.Items) != 1 || subtle.ConstantTimeCompare([]byte(receipt.Account.ID), []byte(expected.AccountID)) != 1 {
		return Verified{}, errors.New("Play subscription ownership or structure is invalid")
	}
	item := receipt.Items[0]
	unpaid := receipt.State == "SUBSCRIPTION_STATE_PENDING" || receipt.State == "SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED"
	if item.ProductID != expected.ProductID || item.Offer.BasePlanID != expected.BasePlanID || item.Offer.OfferID != "" || !unpaid && (item.AutoRenewing == nil) == (item.Prepaid == nil) {
		return Verified{}, errors.New("Play product or plan does not match")
	}
	if receipt.Test != nil && !expected.AllowTest {
		return Verified{}, errors.New("Play test purchase is not eligible")
	}
	if receipt.AcknowledgementState != "ACKNOWLEDGEMENT_STATE_PENDING" && receipt.AcknowledgementState != "ACKNOWLEDGEMENT_STATE_ACKNOWLEDGED" {
		return Verified{}, errors.New("Play acknowledgement state is invalid")
	}
	out := Verified{ProductID: item.ProductID, BasePlanID: item.Offer.BasePlanID, OrderID: item.OrderID, State: receipt.State,
		Test: receipt.Test != nil, AutoRenews: item.AutoRenewing != nil && item.AutoRenewing.Enabled,
		NeedsAcknowledgement: false}
	switch receipt.State {
	case "SUBSCRIPTION_STATE_PENDING":
		out.Pending = true
		return out, nil
	case "SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED":
		out.AutoRenews = false
		return out, nil // Unpaid cancellations may have no expiry timestamp.
	case "SUBSCRIPTION_STATE_ACTIVE", "SUBSCRIPTION_STATE_IN_GRACE_PERIOD", "SUBSCRIPTION_STATE_CANCELED":
		if !strings.HasPrefix(item.OrderID, "GPA.") || len(item.OrderID) > 200 {
			return Verified{}, errors.New("Play successful order is missing")
		}
		out.Active = true
	case "SUBSCRIPTION_STATE_ON_HOLD", "SUBSCRIPTION_STATE_PAUSED", "SUBSCRIPTION_STATE_EXPIRED":
		// A valid inactive receipt must not grant/extend an entitlement.
	default:
		return Verified{}, errors.New("Play subscription state is unsupported")
	}
	var err error
	out.ExpiresAt, err = time.Parse(time.RFC3339Nano, item.Expiry)
	if err != nil {
		return Verified{}, errors.New("Play expiry is invalid")
	}
	out.Active = out.Active && out.ExpiresAt.After(now)
	if receipt.State == "SUBSCRIPTION_STATE_EXPIRED" || receipt.State == "SUBSCRIPTION_STATE_CANCELED" || receipt.State == "SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED" {
		out.AutoRenews = false
	}
	if expected.AmountMinor > 0 && out.Active {
		if (expected.Prepaid && (item.Prepaid == nil || !expected.VerifyOrder)) || (!expected.Prepaid && item.AutoRenewing == nil) || item.Phase.FreeTrial != nil || item.Phase.Introductory != nil {
			return Verified{}, errors.New("Play discounted or prepaid plan is not configured")
		}
		if expected.Prepaid {
			out.AmountMinor = expected.AmountMinor
			out.NeedsAcknowledgement = receipt.AcknowledgementState == "ACKNOWLEDGEMENT_STATE_PENDING"
			return out, nil
		}
		price := item.AutoRenewing.Price
		units, e := strconv.ParseInt(price.Units, 10, 64)
		if e != nil || units < 0 || units > 999999999999 || price.Nanos < 0 || price.Nanos >= 1000000000 || price.Nanos%10000000 != 0 || price.Currency != "NGN" || units*100+price.Nanos/10000000 != expected.AmountMinor {
			return Verified{}, errors.New("Play price does not match the configured plan")
		}
		out.AmountMinor = expected.AmountMinor
	}
	out.NeedsAcknowledgement = out.Active && receipt.AcknowledgementState == "ACKNOWLEDGEMENT_STATE_PENDING"
	return out, nil
}
