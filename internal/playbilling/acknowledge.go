package playbilling

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Acknowledge rechecks Google state rather than trusting a mobile acknowledgement
// claim. Call only after the verified purchase has been durably committed. A
// failed response is retryable by reconciliation; it must not mint access again.
func (c *Client) Acknowledge(ctx context.Context, token string, expected Expected, now time.Time) (Verified, error) {
	receipt, err := c.Verify(ctx, token, expected, now)
	if err != nil {
		return Verified{}, err
	}
	if !receipt.Active {
		return receipt, errors.New("Play purchase is not ready for acknowledgement")
	}
	if !receipt.NeedsAcknowledgement {
		return receipt, nil
	}
	access, err := c.token(ctx)
	if err != nil || access == "" || strings.ContainsAny(access, "\r\n") {
		return receipt, errors.New("Play authentication is unavailable")
	}
	endpoint := "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/" + PackageName + "/purchases/subscriptions/" + url.PathEscape(expected.ProductID) + "/tokens/" + url.PathEscape(token) + ":acknowledge"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		return receipt, errors.New("Play acknowledgement request is unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+access)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return receipt, errors.New("Play acknowledgement is not confirmed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return receipt, errors.New("Play acknowledgement is not confirmed")
	}
	receipt.NeedsAcknowledgement = false
	return receipt, nil
}
