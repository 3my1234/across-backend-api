package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	emailBatchLimit  = 25
	emailConcurrency = 4
	maxEmailAttempts = 6
)

var ErrRecipientSuppressed = errors.New("email recipient is suppressed")

type emailQueryer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type emailDelivery struct {
	ID             string
	RecipientEmail string
	RecipientName  string
	TemplateType   string
	Payload        json.RawMessage
	Attempts       int
}

func QueueVerificationEmail(ctx context.Context, db emailQueryer, userID, email, name, token, publicBaseURL string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	payload := map[string]string{
		"verification_url": baseURL + "/api/v1/auth/verify-email?token=" + url.QueryEscape(token),
	}
	return queueEmail(ctx, db, "verification:"+userID+":"+tokenDigest(token), email, name, "verification", payload)
}

func QueueWelcomeEmail(ctx context.Context, db emailQueryer, userID, email, name string) error {
	return queueEmail(ctx, db, "welcome:"+userID, email, name, "welcome", map[string]string{})
}

func QueuePasswordResetEmail(ctx context.Context, db emailQueryer, userID, email, name, token, publicBaseURL string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	payload := map[string]string{
		"reset_url": baseURL + "/api/v1/auth/reset-password?token=" + url.QueryEscape(token),
	}
	return queueEmail(ctx, db, "password-reset:"+userID+":"+tokenDigest(token), email, name, "password_reset", payload)
}

func queueEmail(ctx context.Context, db emailQueryer, dedupeKey, recipientEmail, recipientName, templateType string, payload any) error {
	email := strings.ToLower(strings.TrimSpace(recipientEmail))
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var status string
	err = db.QueryRow(ctx, `
		INSERT INTO email_outbox(dedupe_key, recipient_email, recipient_name, template_type, payload, status)
		VALUES ($1, $2, $3, $4, $5::jsonb,
			CASE WHEN EXISTS (SELECT 1 FROM email_suppressions WHERE email = $2) THEN 'suppressed' ELSE 'pending' END)
		ON CONFLICT (dedupe_key) DO UPDATE SET dedupe_key = EXCLUDED.dedupe_key
		RETURNING status
	`, dedupeKey, email, strings.TrimSpace(recipientName), templateType, encoded).Scan(&status)
	if err != nil {
		return err
	}
	if status == "suppressed" {
		return ErrRecipientSuppressed
	}
	return nil
}

func RunEmailDeliveryBatch(ctx context.Context, db *pgxpool.Pool, sender *EmailService) (int, error) {
	if sender == nil {
		return 0, errors.New("email sender is nil")
	}
	if _, err := db.Exec(ctx, `
		UPDATE email_outbox
		SET status = 'retry', next_attempt_at = now(), locked_at = NULL,
			last_error = 'delivery lease expired', updated_at = now()
		WHERE status = 'sending' AND locked_at < now() - interval '5 minutes'
	`); err != nil {
		return 0, err
	}
	if _, err := db.Exec(ctx, `
		UPDATE email_outbox o
		SET status = 'suppressed', locked_at = NULL,
			last_error = 'recipient is on the email suppression list', updated_at = now()
		WHERE o.status IN ('pending', 'retry')
		  AND EXISTS (SELECT 1 FROM email_suppressions s WHERE s.email = o.recipient_email)
	`); err != nil {
		return 0, err
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id::text, recipient_email, recipient_name, template_type, payload, attempts
		FROM email_outbox
		WHERE status IN ('pending', 'retry') AND next_attempt_at <= now()
		ORDER BY next_attempt_at, created_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`, emailBatchLimit)
	if err != nil {
		return 0, err
	}
	items := make([]emailDelivery, 0, emailBatchLimit)
	for rows.Next() {
		var item emailDelivery
		if err := rows.Scan(&item.ID, &item.RecipientEmail, &item.RecipientName, &item.TemplateType, &item.Payload, &item.Attempts); err != nil {
			rows.Close()
			return 0, err
		}
		item.Attempts++
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, item := range items {
		if _, err := tx.Exec(ctx, `
			UPDATE email_outbox
			SET status = 'sending', attempts = attempts + 1, locked_at = now(), updated_at = now()
			WHERE id = $1::uuid
		`, item.ID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, nil
	}

	jobs := make(chan emailDelivery)
	results := make(chan error, len(items))
	var workers sync.WaitGroup
	for i := 0; i < emailConcurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				results <- deliverEmail(ctx, db, sender, item)
			}
		}()
	}
	go func() {
		for _, item := range items {
			jobs <- item
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	completed := 0
	var firstErr error
	for deliveryErr := range results {
		if deliveryErr != nil && firstErr == nil {
			firstErr = deliveryErr
		}
		if deliveryErr == nil {
			completed++
		}
	}
	return completed, firstErr
}

func deliverEmail(ctx context.Context, db *pgxpool.Pool, sender *EmailService, item emailDelivery) error {
	var suppressed bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM email_suppressions WHERE email = $1)`, item.RecipientEmail).Scan(&suppressed); err != nil {
		return retryEmail(ctx, db, item, err.Error())
	}
	if suppressed {
		_, err := db.Exec(ctx, `
			UPDATE email_outbox SET status = 'suppressed', locked_at = NULL,
				last_error = 'recipient is on the email suppression list', updated_at = now()
			WHERE id = $1::uuid
		`, item.ID)
		return err
	}
	if err := sender.SendOutboxTemplate(item.RecipientEmail, item.RecipientName, item.TemplateType, item.Payload, item.ID); err != nil {
		return retryEmail(ctx, db, item, err.Error())
	}
	_, err := db.Exec(ctx, `
		UPDATE email_outbox SET status = 'sent', sent_at = now(), locked_at = NULL,
			last_error = '', updated_at = now()
		WHERE id = $1::uuid
	`, item.ID)
	return err
}

func retryEmail(ctx context.Context, db *pgxpool.Pool, item emailDelivery, message string) error {
	status := "retry"
	if item.Attempts >= maxEmailAttempts {
		status = "failed"
	}
	delay := 30 * time.Second * time.Duration(1<<min(item.Attempts-1, 9))
	if delay > 6*time.Hour {
		delay = 6 * time.Hour
	}
	_, err := db.Exec(ctx, `
		UPDATE email_outbox
		SET status = $2, next_attempt_at = now() + $3::interval, locked_at = NULL,
			last_error = left($4::text, 500), updated_at = now()
		WHERE id = $1::uuid
	`, item.ID, status, fmt.Sprintf("%f seconds", delay.Seconds()), message)
	if err != nil {
		return err
	}
	log.Printf("email outbox delivery deferred id=%s template=%s attempt=%d status=%s", item.ID, item.TemplateType, item.Attempts, status)
	return nil
}

func tokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
