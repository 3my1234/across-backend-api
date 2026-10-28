package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const receiptLeaseSeconds = 60

type receiptClaim struct {
	pushDelivery
	Attempts int
}

// RunPushReceiptBatch checks at most 100 receipts, retaining the historical
// return value: successfully persisted terminal receipt outcomes, not requests.
func RunPushReceiptBatch(ctx context.Context, db *pgxpool.Pool, client *http.Client) (int, error) {
	checked, _, err := runPushReceiptBatch(ctx, db, client)
	return checked, err
}

// RunPushReceiptDrain has a per-tick budget independent of push sending. The
// database gate also bounds requests across replicas; adding pods cannot multiply
// Expo traffic. Five batches/tick replace the old single-batch throughput cap.
func RunPushReceiptDrain(ctx context.Context, db *pgxpool.Pool, client *http.Client) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	total := 0
	for batch := 0; batch < 5; batch++ {
		checked, claimed, err := runPushReceiptBatch(ctx, db, client)
		total += checked
		if err != nil || claimed < pushBatchLimit {
			return total, err
		}
		if batch < 4 {
			timer := time.NewTimer(time.Second)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return total, ctx.Err()
			}
		}
	}
	return total, nil
}

func claimPushReceipts(ctx context.Context, db *pgxpool.Pool, owner string) ([]receiptClaim, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	gate, err := tx.Exec(ctx, `UPDATE push_receipt_dispatch
		SET owner=$1::uuid, lease_until=clock_timestamp()+interval '60 seconds',
		    next_attempt_at=clock_timestamp()+interval '1 second'
		WHERE singleton AND (lease_until IS NULL OR lease_until <= clock_timestamp())
		  AND next_attempt_at <= clock_timestamp()`, owner)
	if err != nil || gate.RowsAffected() == 0 {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH due AS (
		SELECT notification_id,push_token_id FROM notification_push_deliveries
		WHERE status='sent' AND receipt_checked_at IS NULL AND expo_ticket_id IS NOT NULL
		  AND sent_at <= clock_timestamp()-interval '15 minutes'
		  AND receipt_next_attempt_at <= clock_timestamp()
		  AND (receipt_lease_until IS NULL OR receipt_lease_until <= clock_timestamp())
		ORDER BY receipt_next_attempt_at,sent_at,notification_id,push_token_id
		FOR UPDATE SKIP LOCKED LIMIT $2
	) UPDATE notification_push_deliveries d
	SET receipt_claim=$1::uuid,receipt_lease_until=clock_timestamp()+interval '60 seconds',
	    receipt_attempts=receipt_attempts+1
	FROM due WHERE d.notification_id=due.notification_id AND d.push_token_id=due.push_token_id
	RETURNING d.notification_id::text,d.push_token_id::text,d.expo_ticket_id,d.receipt_attempts`, owner, pushBatchLimit)
	if err != nil {
		return nil, err
	}
	items := make([]receiptClaim, 0, pushBatchLimit)
	for rows.Next() {
		var item receiptClaim
		if err := rows.Scan(&item.NotificationID, &item.PushTokenID, &item.TicketID, &item.Attempts); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return items, tx.Commit(ctx)
}

func runPushReceiptBatch(ctx context.Context, db *pgxpool.Pool, client *http.Client) (checked, claimed int, resultErr error) {
	// Bound even a caller-supplied client with no Timeout; shorter parent deadlines win.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	owner := uuid.NewString()
	items, err := claimPushReceipts(ctx, db, owner)
	if err != nil {
		return 0, 0, err
	}
	claimed = len(items)
	// Cleanup uses a short independent deadline; if the process dies or DB is
	// unreachable, durable leases recover automatically. Every write is fenced.
	var providerDelay time.Duration
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, cleanErr := db.Exec(cleanup, `UPDATE notification_push_deliveries
			SET receipt_claim=NULL,receipt_lease_until=NULL,
			    receipt_next_attempt_at=clock_timestamp()+make_interval(secs =>
			      LEAST(900,30*power(2,LEAST(receipt_attempts-1,5)))::double precision)
			WHERE receipt_claim=$1::uuid AND receipt_lease_until > clock_timestamp()`, owner)
		if cleanErr == nil {
			_, cleanErr = db.Exec(cleanup, `UPDATE push_receipt_dispatch
				SET owner=NULL,lease_until=NULL,
				    next_attempt_at=GREATEST(next_attempt_at,clock_timestamp()+make_interval(secs => $2::double precision),
				      CASE WHEN $3 THEN clock_timestamp()+make_interval(secs => LEAST(900,5*power(2,LEAST(failures,8)))::double precision) ELSE '-infinity'::timestamptz END),
				    failures=CASE WHEN $3 THEN LEAST(failures+1,30) ELSE 0 END
				WHERE singleton AND owner=$1::uuid AND lease_until > clock_timestamp()`, owner, providerDelay.Seconds(), resultErr != nil)
		}
		if resultErr == nil {
			resultErr = cleanErr
		}
	}()
	if claimed == 0 {
		return 0, 0, nil
	}
	ids := make([]string, 0, claimed)
	for _, item := range items {
		ids = append(ids, item.TicketID)
	}
	payload, _ := json.Marshal(map[string]any{"ids": ids})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, expoPushReceiptsURL, bytes.NewReader(payload))
	if err != nil {
		return 0, claimed, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, claimed, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		providerDelay = receiptRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		return 0, claimed, fmt.Errorf("Expo receipt HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return 0, claimed, err
	}
	if len(body) > 1<<20 {
		return 0, claimed, fmt.Errorf("Expo receipt response exceeds limit")
	}
	var decoded struct {
		Data map[string]expoTicket `json:"data"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return 0, claimed, fmt.Errorf("invalid Expo receipt response")
	}
	if decoded.Data == nil {
		return 0, claimed, fmt.Errorf("missing Expo receipt data")
	}
	for _, item := range items {
		receipt, ok := decoded.Data[item.TicketID]
		if !ok {
			continue
		} // Cleanup backs off unresolved receipts; no oldest-row starvation.
		if receipt.Status != "ok" && receipt.Status != "error" {
			return checked, claimed, fmt.Errorf("invalid Expo receipt status")
		}
		updated, err := finishPushReceipt(ctx, db, owner, item, receipt)
		if err != nil {
			return checked, claimed, err
		}
		if updated {
			checked++
		}
	}
	return checked, claimed, nil
}

func finishPushReceipt(ctx context.Context, db *pgxpool.Pool, owner string, item receiptClaim, receipt expoTicket) (bool, error) {
	status, message := "delivered", ""
	if receipt.Status == "error" {
		status, message = "failed", receipt.Details.Error+": "+receipt.Message
		if receipt.Details.Error == "DeviceNotRegistered" {
			status = "disabled"
		}
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE notification_push_deliveries
		SET status=$5,receipt_checked_at=clock_timestamp(),updated_at=clock_timestamp(),
		    receipt_claim=NULL,receipt_lease_until=NULL,last_error=left($6::text,500)
		WHERE notification_id=$1::uuid AND push_token_id=$2::uuid AND expo_ticket_id=$3
		  AND status='sent' AND receipt_checked_at IS NULL AND receipt_claim=$4::uuid
		  AND receipt_lease_until > clock_timestamp()`, item.NotificationID, item.PushTokenID, item.TicketID, owner, status, message)
	if err != nil || result.RowsAffected() == 0 {
		return false, err
	}
	if status == "disabled" {
		_, err = tx.Exec(ctx, `UPDATE user_push_tokens SET disabled_at=now(),updated_at=now() WHERE id=$1::uuid`, item.PushTokenID)
		if err != nil {
			return false, err
		}
	}
	err = tx.Commit(ctx)
	return err == nil, err
}

func receiptRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}
