package playbilling

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type googleMoney struct {
	Currency string `json:"currencyCode"`
	Units    string `json:"units"`
	Nanos    int64  `json:"nanos"`
}

func (m googleMoney) minor() (int64, error) {
	units, err := strconv.ParseInt(m.Units, 10, 64)
	if err != nil || m.Currency != "NGN" || units < 0 || units > 999999999999 || m.Nanos < 0 || m.Nanos >= 1000000000 || m.Nanos%10000000 != 0 {
		return 0, errors.New("Play order price is invalid")
	}
	return units*100 + m.Nanos/10000000, nil
}
func (c *Client) verifyOrder(ctx context.Context, token string, receipt Verified, expected Expected) (int64, error) {
	access, err := c.token(ctx)
	if err != nil || access == "" || strings.ContainsAny(access, "\r\n") {
		return 0, errors.New("Play order authentication is unavailable")
	}
	endpoint := "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/" + PackageName + "/orders/" + url.PathEscape(receipt.OrderID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, errors.New("Play order request is unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+access)
	response, err := c.http.Do(request)
	if err != nil {
		return 0, errors.New("Play order verification is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return 0, errors.New("Play order is not confirmed")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return 0, errors.New("Play order response is invalid")
	}
	var order struct {
		ID    string      `json:"orderId"`
		Token string      `json:"purchaseToken"`
		State string      `json:"state"`
		Total googleMoney `json:"total"`
		Items []struct {
			Product      string      `json:"productId"`
			Price        googleMoney `json:"listingPrice"`
			Subscription *struct {
				Base    string `json:"basePlanId"`
				Offer   string `json:"offerId"`
				Phase   string `json:"offerPhase"`
				Details struct {
					Base *struct{} `json:"baseDetails"`
				} `json:"offerPhaseDetails"`
			} `json:"subscriptionDetails"`
		} `json:"lineItems"`
	}
	if json.Unmarshal(raw, &order) != nil || order.ID != receipt.OrderID || subtle.ConstantTimeCompare([]byte(order.Token), []byte(token)) != 1 || order.State != "PROCESSED" || len(order.Items) != 1 {
		return 0, errors.New("Play order ownership or payment state is invalid")
	}
	item := order.Items[0]
	if item.Product != expected.ProductID || item.Subscription == nil || item.Subscription.Base != expected.BasePlanID || item.Subscription.Offer != "" || (item.Subscription.Phase != "BASE" && item.Subscription.Details.Base == nil) {
		return 0, errors.New("Play order product or phase is invalid")
	}
	listed, err := item.Price.minor()
	if err != nil || listed != expected.AmountMinor {
		return 0, errors.New("Play order listed price does not match")
	}
	paid, err := order.Total.minor()
	if err != nil || paid < 0 {
		return 0, errors.New("Play order payment amount is invalid")
	}
	if paid == 0 {
		if receipt.Test && expected.AllowTest {
			return expected.AmountMinor, nil
		} // Test-only face value; never a financial receipt or referral credit.
		return 0, errors.New("Play order payment amount is invalid")
	}
	return paid, nil
}
