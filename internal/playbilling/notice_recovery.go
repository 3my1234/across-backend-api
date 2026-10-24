package playbilling

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// identifyNotification obtains routing metadata only from Google's authenticated
// API. It grants nothing; Process subsequently verifies the complete purchase.
func (c *Client) identifyNotification(ctx context.Context, token string) (string, string, string, error) {
	invalid := errors.New("Play notification purchase could not be identified")
	if !validToken(token) {
		return "", "", "", invalid
	}
	access, err := c.token(ctx)
	if err != nil || access == "" || strings.ContainsAny(access, "\r\n") {
		return "", "", "", invalid
	}
	endpoint := "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/" + PackageName + "/purchases/subscriptionsv2/tokens/" + url.PathEscape(token)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", "", invalid
	}
	request.Header.Set("Authorization", "Bearer "+access)
	response, err := c.http.Do(request)
	if err != nil {
		return "", "", "", invalid
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", "", "", invalid
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return "", "", "", invalid
	}
	var receipt purchase
	if json.Unmarshal(raw, &receipt) != nil || receipt.Kind != "androidpublisher#subscriptionPurchaseV2" || len(receipt.Items) != 1 || !accountPattern.MatchString(receipt.Account.ID) || !skuPattern.MatchString(receipt.Items[0].ProductID) || !skuPattern.MatchString(receipt.Items[0].Offer.BasePlanID) {
		return "", "", "", invalid
	}
	return receipt.Account.ID, receipt.Items[0].ProductID, receipt.Items[0].Offer.BasePlanID, nil
}

func (s *Store) recoverNotificationPurchases(ctx context.Context) error {
	rows, err := s.DB.Query(ctx, `WITH due AS(SELECT e.message_id FROM play_provider_events e WHERE e.processed_at IS NULL AND e.next_attempt_at<=now() AND NOT EXISTS(SELECT 1 FROM play_provider_purchases p WHERE p.token_hash=e.token_hash) ORDER BY e.received_at LIMIT 10 FOR UPDATE SKIP LOCKED)
 UPDATE play_provider_events e SET next_attempt_at=now()+interval '5 minutes' FROM due WHERE due.message_id=e.message_id RETURNING e.message_id,e.encrypted_token`)
	if err != nil {
		return errors.New("Play initial-purchase recovery is unavailable")
	}
	type item struct {
		id        string
		encrypted []byte
	}
	items := []item{}
	for rows.Next() {
		var value item
		if rows.Scan(&value.id, &value.encrypted) != nil {
			rows.Close()
			return errors.New("Play initial-purchase recovery is unavailable")
		}
		items = append(items, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return errors.New("Play initial-purchase recovery is unavailable")
	}
	for _, value := range items {
		token, e := s.Vault.Open("rtdn:"+value.id, value.encrypted)
		if e != nil {
			continue
		}
		account, product, base, e := s.Client.identifyNotification(ctx, token)
		if e != nil {
			continue
		}
		var provider string
		if e = s.DB.QueryRow(ctx, `SELECT provider_id::text FROM play_provider_accounts WHERE account_id=$1`, account).Scan(&provider); e != nil {
			continue
		}
		if _, e = s.Process(ctx, provider, token, product, base); e != nil {
			continue
		}
		_, _ = s.DB.Exec(ctx, `UPDATE play_provider_events SET processed_at=now() WHERE message_id=$1`, value.id)
	}
	return nil
}
