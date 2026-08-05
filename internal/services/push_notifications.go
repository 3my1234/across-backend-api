package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	expoPushSendURL     = "https://exp.host/--/api/v2/push/send"
	expoPushReceiptsURL = "https://exp.host/--/api/v2/push/getReceipts"
	pushBatchLimit      = 100
	maxPushAttempts     = 6
)

type pushDelivery struct {
	NotificationID string
	OrderID        string
	PushTokenID    string
	Token          string
	Title          string
	Body           string
	Data           json.RawMessage
	Attempts       int
	TicketID       string
}

func RunPushReceiptBatch(ctx context.Context, db *pgxpool.Pool, client *http.Client) (int, error) {
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
			err = disablePushToken(ctx, db, item, receipt.Message)
		} else {
			_, err = db.Exec(ctx, `
				UPDATE notification_push_deliveries
				SET status = 'failed', receipt_checked_at = now(),
					last_error = left($3::text, 500), updated_at = now()
				WHERE notification_id = $1::uuid AND push_token_id = $2::uuid
			`, item.NotificationID, item.PushTokenID, receipt.Message)
		}
		if err != nil {
			return checked, err
		}
	}
	return checked, nil
}

type expoTicket struct {
	Status  string `json:"status"`
	ID      string `json:"id"`
	Message string `json:"message"`
	Details struct {
		Error string `json:"error"`
	} `json:"details"`
}

func RunPushDeliveryBatch(ctx context.Context, db *pgxpool.Pool, client *http.Client) (int, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	// Recover jobs left in-flight by a stopped replica and materialize delivery
	// rows idempotently. The recent-notification cap keeps each worker pass bounded.
	if _, err := db.Exec(ctx, `
		UPDATE notification_push_deliveries
		SET status = 'retry', next_attempt_at = now(), updated_at = now(),
			last_error = 'delivery lease expired'
		WHERE status = 'sending' AND updated_at < now() - interval '5 minutes'
	`); err != nil {
		return 0, err
	}
	if _, err := db.Exec(ctx, `
		WITH recent AS (
			SELECT id, user_id
			FROM notifications
			WHERE created_at >= now() - interval '48 hours' AND is_read = false
			ORDER BY created_at DESC, id DESC
			LIMIT 500
		)
		INSERT INTO notification_push_deliveries(notification_id, push_token_id)
		SELECT recent.id, token.id
		FROM recent
		JOIN user_push_tokens token ON token.user_id = recent.user_id AND token.disabled_at IS NULL
		ON CONFLICT DO NOTHING
	`); err != nil {
		return 0, err
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT d.notification_id::text, COALESCE(n.order_id::text, ''), d.push_token_id::text, token.expo_push_token,
			n.title, n.body, n.data, d.attempts
		FROM notification_push_deliveries d
		JOIN notifications n ON n.id = d.notification_id
		JOIN user_push_tokens token ON token.id = d.push_token_id AND token.disabled_at IS NULL
		WHERE d.status IN ('pending', 'retry') AND d.next_attempt_at <= now()
		ORDER BY d.next_attempt_at, d.notification_id, d.push_token_id
		FOR UPDATE OF d SKIP LOCKED
		LIMIT $1
	`, pushBatchLimit)
	if err != nil {
		return 0, err
	}
	deliveries := make([]pushDelivery, 0, pushBatchLimit)
	for rows.Next() {
		var item pushDelivery
		if err := rows.Scan(&item.NotificationID, &item.OrderID, &item.PushTokenID, &item.Token, &item.Title, &item.Body, &item.Data, &item.Attempts); err != nil {
			rows.Close()
			return 0, err
		}
		deliveries = append(deliveries, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, item := range deliveries {
		if _, err := tx.Exec(ctx, `
			UPDATE notification_push_deliveries
			SET status = 'sending', attempts = attempts + 1, updated_at = now()
			WHERE notification_id = $1::uuid AND push_token_id = $2::uuid
		`, item.NotificationID, item.PushTokenID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	if len(deliveries) == 0 {
		return 0, nil
	}

	messages := make([]map[string]any, 0, len(deliveries))
	for _, item := range deliveries {
		data := map[string]any{}
		_ = json.Unmarshal(item.Data, &data)
		data["notification_id"] = item.NotificationID
		if item.OrderID != "" {
			data["order_id"] = item.OrderID
		}
		messages = append(messages, map[string]any{
			"to": item.Token, "title": item.Title, "body": item.Body,
			"sound": "default", "channelId": "orders", "data": data,
		})
	}
	payload, err := json.Marshal(messages)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, expoPushSendURL, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, retryPushDeliveries(ctx, db, deliveries, err.Error())
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, retryPushDeliveries(ctx, db, deliveries, err.Error())
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return 0, retryPushDeliveries(ctx, db, deliveries, fmt.Sprintf("expo push HTTP %d", resp.StatusCode))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, failPushDeliveries(ctx, db, deliveries, fmt.Sprintf("expo push HTTP %d", resp.StatusCode))
	}
	var decoded struct {
		Data []expoTicket `json:"data"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || len(decoded.Data) != len(deliveries) {
		return 0, retryPushDeliveries(ctx, db, deliveries, "invalid Expo push response")
	}
	for index, ticket := range decoded.Data {
		item := deliveries[index]
		if ticket.Status == "ok" && ticket.ID != "" {
			_, err = db.Exec(ctx, `
				UPDATE notification_push_deliveries
				SET status = 'sent', expo_ticket_id = $3::text, sent_at = now(),
					last_error = '', updated_at = now()
				WHERE notification_id = $1::uuid AND push_token_id = $2::uuid
			`, item.NotificationID, item.PushTokenID, ticket.ID)
		} else if ticket.Details.Error == "DeviceNotRegistered" {
			err = disablePushToken(ctx, db, item, ticket.Message)
		} else {
			err = retryPushDelivery(ctx, db, item, ticket.Message)
		}
		if err != nil {
			return index, err
		}
	}
	return len(deliveries), nil
}

func retryPushDeliveries(ctx context.Context, db *pgxpool.Pool, items []pushDelivery, message string) error {
	for _, item := range items {
		if err := retryPushDelivery(ctx, db, item, message); err != nil {
			return err
		}
	}
	return nil
}

func retryPushDelivery(ctx context.Context, db *pgxpool.Pool, item pushDelivery, message string) error {
	nextAttempt := item.Attempts + 1
	status := "retry"
	if nextAttempt >= maxPushAttempts {
		status = "failed"
	}
	delay := 30 * time.Second * time.Duration(1<<min(nextAttempt-1, 7))
	if delay > time.Hour {
		delay = time.Hour
	}
	_, err := db.Exec(ctx, `
		UPDATE notification_push_deliveries
		SET status = $3::text, next_attempt_at = $4::timestamptz,
			last_error = left($5::text, 500), updated_at = now()
		WHERE notification_id = $1::uuid AND push_token_id = $2::uuid
	`, item.NotificationID, item.PushTokenID, status, time.Now().Add(delay), message)
	return err
}

func failPushDeliveries(ctx context.Context, db *pgxpool.Pool, items []pushDelivery, message string) error {
	for _, item := range items {
		if _, err := db.Exec(ctx, `
			UPDATE notification_push_deliveries
			SET status = 'failed', last_error = left($3::text, 500), updated_at = now()
			WHERE notification_id = $1::uuid AND push_token_id = $2::uuid
		`, item.NotificationID, item.PushTokenID, message); err != nil {
			return err
		}
	}
	return nil
}

func disablePushToken(ctx context.Context, db *pgxpool.Pool, item pushDelivery, message string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE user_push_tokens SET disabled_at = now(), updated_at = now() WHERE id = $1::uuid
	`, item.PushTokenID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE notification_push_deliveries
		SET status = 'disabled', last_error = left($3::text, 500), updated_at = now()
		WHERE notification_id = $1::uuid AND push_token_id = $2::uuid
	`, item.NotificationID, item.PushTokenID, message); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
