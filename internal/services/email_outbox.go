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
	UserID         string
	RecipientEmail string
	RecipientName  string
	TemplateType   string
	Payload        json.RawMessage
	Attempts       int
}

func QueueVerificationEmail(ctx context.Context, db emailQueryer, userID, token, publicBaseURL string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	payload := map[string]string{
		"verification_url": baseURL + "/api/v1/auth/verify-email?token=" + url.QueryEscape(token),
	}
	return queueUserEmail(ctx, db, userID, "verification:"+userID+":"+tokenDigest(token), "verification", payload)
}

func QueueWelcomeEmail(ctx context.Context, db emailQueryer, userID string) error {
	return queueUserEmail(ctx, db, userID, "welcome:"+userID, "welcome", map[string]string{})
}

func QueuePasswordResetEmail(ctx context.Context, db emailQueryer, userID, token, publicBaseURL string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	payload := map[string]string{
		"reset_url": baseURL + "/api/v1/auth/reset-password?token=" + url.QueryEscape(token),
	}
	return queueUserEmail(ctx, db, userID, "password-reset:"+userID+":"+tokenDigest(token), "password_reset", payload)
}

func queueUserEmail(ctx context.Context, db emailQueryer, userID, dedupeKey, templateType string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var status string
	err = db.QueryRow(ctx, `
		WITH account AS (
			SELECT id, lower(btrim(email)) AS email, btrim(full_name) AS full_name
			FROM users WHERE id = $1::uuid
		), inserted AS (
			INSERT INTO email_outbox(user_id, dedupe_key, recipient_email, recipient_name, template_type, payload, status)
			SELECT id, $2, email, full_name, $3, $4::jsonb,
				CASE WHEN EXISTS (SELECT 1 FROM email_suppressions WHERE email = account.email) THEN 'suppressed' ELSE 'pending' END
			FROM account
			ON CONFLICT (dedupe_key) DO NOTHING
			RETURNING status
		)
		SELECT status FROM inserted
		UNION ALL
		SELECT status FROM email_outbox
		WHERE dedupe_key = $2 AND user_id = $1::uuid
		  AND recipient_email = (SELECT email FROM account)
		LIMIT 1
	`, userID, dedupeKey, templateType, encoded).Scan(&status)
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
		SELECT id::text, user_id::text, recipient_email, recipient_name, template_type, payload, attempts
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
		if err := rows.Scan(&item.ID, &item.UserID, &item.RecipientEmail, &item.RecipientName, &item.TemplateType, &item.Payload, &item.Attempts); err != nil {
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
	var currentEmail, currentName string
	if err := db.QueryRow(ctx, `SELECT lower(btrim(email)), btrim(full_name) FROM users WHERE id = $1::uuid`, item.UserID).Scan(&currentEmail, &currentName); err != nil {
		return failEmail(ctx, db, item, "email owner no longer exists")
	}
	if currentEmail != strings.ToLower(strings.TrimSpace(item.RecipientEmail)) {
		return failEmail(ctx, db, item, "email recipient no longer belongs to the queued account")
	}
	item.RecipientEmail = currentEmail
	item.RecipientName = currentName
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
	recipientDigest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(item.RecipientEmail))))
	log.Printf("email outbox delivered id=%s user_id=%s template=%s recipient_hash=%s", item.ID, item.UserID, item.TemplateType, hex.EncodeToString(recipientDigest[:8]))
	_, err := db.Exec(ctx, `
		UPDATE email_outbox SET status = 'sent', sent_at = now(), locked_at = NULL,
			last_error = '', updated_at = now()
		WHERE id = $1::uuid
	`, item.ID)
	return err
}

func failEmail(ctx context.Context, db *pgxpool.Pool, item emailDelivery, message string) error {
	_, err := db.Exec(ctx, `
		UPDATE email_outbox
		SET status = 'failed', locked_at = NULL, last_error = left($2::text, 500), updated_at = now()
		WHERE id = $1::uuid
	`, item.ID, message)
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
