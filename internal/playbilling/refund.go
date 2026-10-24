package playbilling

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RefundUnfulfilled is used only by a durable, server-owned rejection job for a
// purchase that never received an entitlement. It cannot be called from a client
// cancellation request and independently verifies the same provider/product.
func (c *Client) RefundUnfulfilled(ctx context.Context, token string, expected Expected) error {
	_, err := c.Verify(ctx, token, expected, time.Now().UTC())
	if err != nil {
		return errors.New("Play refund verification is unavailable")
	}
	access, err := c.token(ctx)
	if err != nil || access == "" || strings.ContainsAny(access, "\r\n") {
		return errors.New("Play refund authentication is unavailable")
	}
	endpoint := "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/" + PackageName + "/purchases/subscriptionsv2/tokens/" + url.PathEscape(token) + ":revoke"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(`{"revocationContext":{"fullRefund":{}}}`))
	if err != nil {
		return errors.New("Play refund request is unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+access)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return errors.New("Play refund is not confirmed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 204 {
		return errors.New("Play refund is not confirmed")
	}
	return nil
}

func (s *Store) reconcileRefunds(ctx context.Context) error {
	rows, err := s.DB.Query(ctx, `WITH due AS(SELECT token_hash FROM play_provider_refund_jobs WHERE status='requested' AND next_attempt_at<=now() ORDER BY next_attempt_at LIMIT 10 FOR UPDATE SKIP LOCKED)
 UPDATE play_provider_refund_jobs j SET next_attempt_at=now()+interval '5 minutes' FROM due WHERE j.token_hash=due.token_hash
 RETURNING j.token_hash,j.provider_id::text,j.encrypted_token,j.product_id,j.base_plan_id`)
	if err != nil {
		return errors.New("Play refund queue is unavailable")
	}
	type job struct {
		hash, provider, product, base string
		encrypted                     []byte
	}
	jobs := []job{}
	for rows.Next() {
		var item job
		if rows.Scan(&item.hash, &item.provider, &item.encrypted, &item.product, &item.base) != nil {
			rows.Close()
			return errors.New("Play refund queue is unavailable")
		}
		jobs = append(jobs, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return errors.New("Play refund queue is unavailable")
	}
	for _, item := range jobs {
		var granted bool
		if err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM play_provider_purchases WHERE token_hash=$1)`, item.hash).Scan(&granted); err != nil || granted {
			continue
		}
		token, e := s.Vault.Open(item.provider, item.encrypted)
		if e != nil {
			continue
		}
		e = s.Client.RefundUnfulfilled(ctx, token, Expected{ProductID: item.product, BasePlanID: item.base, AccountID: AccountID(item.provider), AllowTest: s.AllowTest})
		if e == nil {
			_, _ = s.DB.Exec(ctx, `UPDATE play_provider_refund_jobs SET status='confirmed' WHERE token_hash=$1 AND provider_id=$2::uuid`, item.hash, item.provider)
		}
	}
	return nil
}
