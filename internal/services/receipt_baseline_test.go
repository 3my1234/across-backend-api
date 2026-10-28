// Exact historical receipt function from 3600673b54c1674675ffccf696ed9df21edaf0ca.
// Test-only baseline; never linked into an application executable.
package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log"
	"net/http"
	"time"
)

func runLegacyReceiptBatch(ctx context.Context, db *pgxpool.Pool, client *http.Client) (int, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	rows, err := db.Query(ctx, `
		SELECT notification_id::text, push_token_id::text, expo_ticket_id
		FROM notification_push_deliveries
		WHERE status = 'sent' AND receipt_checked_at IS NULL
		  AND expo_ticket_id IS NOT NULL AND sent_at <= now() - interval '15 minutes'
		ORDER BY sent_at, notification_id, push_token_id
		LIMIT $1
	`, pushBatchLimit)
	if err != nil {
		return 0, err
	}
	items := make([]pushDelivery, 0, pushBatchLimit)
	ids := make([]string, 0, pushBatchLimit)
	for rows.Next() {
		var item pushDelivery
		if err := rows.Scan(&item.NotificationID, &item.PushTokenID, &item.TicketID); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
		ids = append(ids, item.TicketID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(items) == 0 {
		return 0, nil
	}
	payload, _ := json.Marshal(map[string]any{"ids": ids})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, expoPushReceiptsURL, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("Expo receipt HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		Data map[string]expoTicket `json:"data"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return 0, err
	}
	checked := 0
	for _, item := range items {
		receipt, ok := decoded.Data[item.TicketID]
		if !ok {
			continue
		}
		checked++
		if receipt.Status == "ok" {
			_, err = db.Exec(ctx, `
				UPDATE notification_push_deliveries
				SET status = 'delivered', receipt_checked_at = now(), updated_at = now()
				WHERE notification_id = $1::uuid AND push_token_id = $2::uuid
			`, item.NotificationID, item.PushTokenID)
		} else if receipt.Details.Error == "DeviceNotRegistered" {
			log.Printf("push receipt rejected: notification=%s code=%s", item.NotificationID, receipt.Details.Error)
			err = disablePushToken(ctx, db, item, receipt.Message)
		} else {
			log.Printf("push receipt rejected: notification=%s code=%s", item.NotificationID, receipt.Details.Error)
			_, err = db.Exec(ctx, `
				UPDATE notification_push_deliveries
				SET status = 'failed', receipt_checked_at = now(),
					last_error = left($3::text, 500), updated_at = now()
				WHERE notification_id = $1::uuid AND push_token_id = $2::uuid
			`, item.NotificationID, item.PushTokenID, receipt.Details.Error+": "+receipt.Message)
		}
		if err != nil {
			return checked, err
		}
	}
	return checked, nil
}
