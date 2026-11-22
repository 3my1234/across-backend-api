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

var disabledEmailCounts = struct {
	sync.Mutex
	since  time.Time
	counts map[string]int64
}{counts: make(map[string]int64)}

// Reports aggregate counts only; recipient addresses and message contents are never logged.
func recordDisabledEmails(counts map[string]int64, now time.Time) {
	disabledEmailCounts.Lock()
	defer disabledEmailCounts.Unlock()
	if disabledEmailCounts.since.IsZero() {
		disabledEmailCounts.since = now
	}
	for category, n := range counts {
		disabledEmailCounts.counts[category] += n
	}
	if now.Sub(disabledEmailCounts.since) >= time.Minute {
		for category, n := range disabledEmailCounts.counts {
			log.Printf("email_disabled_suppressed category=%s count=%d", category, n)
		}
		clear(disabledEmailCounts.counts)
		disabledEmailCounts.since = now
	}
}

type emailQueryer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type emailDelivery struct {
	ID             string
	UserID         *string
	RecipientEmail string
	RecipientName  string
	TemplateType   string
	Payload        json.RawMessage
	Attempts       int
}

func QueueMarketplaceEmail(ctx context.Context, db emailQueryer, userID, dedupeKey string, payload map[string]string) error {
	return queueUserEmail(ctx, db, userID, dedupeKey, "marketplace_activity", payload)
}

func QueueDirectMarketplaceEmail(ctx context.Context, db emailQueryer, recipientEmail, recipientName, dedupeKey string, payload map[string]string) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(recipientEmail))
	if email == "" {
		return errors.New("recipient email is required")
	}
	var status string
	err = db.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO email_outbox(dedupe_key, recipient_email, recipient_name, template_type, payload, status)
			VALUES ($1, $2, btrim($3), 'marketplace_activity', $4::jsonb,
				CASE WHEN EXISTS (SELECT 1 FROM email_suppressions WHERE email = $2) THEN 'suppressed' ELSE 'pending' END)
			ON CONFLICT (dedupe_key) DO NOTHING
			RETURNING status
		)
		SELECT status FROM inserted
		UNION ALL
		SELECT status FROM email_outbox WHERE dedupe_key = $1 AND recipient_email = $2
		LIMIT 1
	`, dedupeKey, email, recipientName, encoded).Scan(&status)
	if err != nil {
		return err
	}
	if status == "suppressed" {
		return ErrRecipientSuppressed
	}
	return nil
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
	return QueueWelcomeEmailForContext(ctx, db, userID, "buyer")
}

func QueueWelcomeEmailForContext(ctx context.Context, db emailQueryer, userID, accountContext string) error {
	if accountContext != "provider" {
		accountContext = "buyer"
	}
	return queueUserEmail(ctx, db, userID, "welcome:"+userID, "welcome", map[string]string{
		"account_context": accountContext,
	})
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
	// Older API instances may still enqueue these while the rollout is in
	// progress. Retain the rows for audit, but never claim them for delivery.
	rowsSuppressed, err := db.Query(ctx, `WITH changed AS (UPDATE email_outbox SET status = 'suppressed', locked_at = NULL,
		last_error = 'email category disabled', updated_at = now()
		WHERE status IN ('pending', 'retry') AND (template_type = 'welcome' OR
		(template_type = 'marketplace_activity' AND (dedupe_key LIKE 'provider-message:%' OR
		dedupe_key LIKE 'provider-request:%' OR dedupe_key LIKE 'provider-request-email:%' OR
		dedupe_key LIKE 'listing-submitted:%' OR dedupe_key LIKE 'merchant-product-submitted:%'))) RETURNING template_type, dedupe_key)
		SELECT CASE WHEN template_type='welcome' THEN 'welcome'
		WHEN dedupe_key LIKE 'provider-message:%' THEN 'chat'
		WHEN dedupe_key LIKE 'provider-request:%' THEN 'provider_request'
		WHEN dedupe_key LIKE 'provider-request-email:%' THEN 'request_status'
		WHEN dedupe_key LIKE 'listing-submitted:%' THEN 'service_submission'
		ELSE 'product_submission' END, count(*) FROM changed GROUP BY 1`)
	if err != nil {
		return 0, err
	}
	counts := make(map[string]int64)
	for rowsSuppressed.Next() {
		var category string
		var count int64
		if err := rowsSuppressed.Scan(&category, &count); err != nil {
			rowsSuppressed.Close()
			return 0, err
		}
		counts[category] = count
	}
	err = rowsSuppressed.Err()
	rowsSuppressed.Close()
	if err != nil {
		return 0, err
	}
	recordDisabledEmails(counts, time.Now())
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
	// Reserve five of every 25 claims for non-security email when present.
	// Fill unused slots from either class so small queues do not waste capacity.
	items := make([]emailDelivery, 0, emailBatchLimit)
	claim := func(security bool, limit int) error {
		if limit <= 0 {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT id::text, user_id::text, recipient_email, recipient_name, template_type, payload, attempts
    FROM email_outbox WHERE status IN ('pending','retry') AND next_attempt_at <= now()
    AND NOT (template_type = 'welcome' OR (template_type = 'marketplace_activity' AND (dedupe_key LIKE 'provider-message:%' OR dedupe_key LIKE 'provider-request:%' OR dedupe_key LIKE 'provider-request-email:%' OR dedupe_key LIKE 'listing-submitted:%' OR dedupe_key LIKE 'merchant-product-submitted:%')))
    AND (template_type IN ('verification','password_reset')) = $1
    ORDER BY next_attempt_at,created_at,id FOR UPDATE SKIP LOCKED LIMIT $2`, security, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item emailDelivery
			if err := rows.Scan(&item.ID, &item.UserID, &item.RecipientEmail, &item.RecipientName, &item.TemplateType, &item.Payload, &item.Attempts); err != nil {
				return err
			}
			item.Attempts++
			items = append(items, item)
		}
		return rows.Err()
	}
	if err := claim(true, 20); err != nil {
		return 0, err
	}
	if err := claim(false, 5); err != nil {
		return 0, err
	}
	// Mark claimed rows before asking for a second page: otherwise the same
	// transaction could select its own locked rows again.
	for _, item := range items {
		if _, err := tx.Exec(ctx, `UPDATE email_outbox SET status='sending',attempts=attempts+1,locked_at=now(),updated_at=now() WHERE id=$1::uuid`, item.ID); err != nil {
			return 0, err
		}
	}
	first := len(items)
	if err := claim(true, emailBatchLimit-len(items)); err != nil {
		return 0, err
	}
	if err := claim(false, emailBatchLimit-len(items)); err != nil {
		return 0, err
	}
	for _, item := range items[first:] {
		if _, err := tx.Exec(ctx, `UPDATE email_outbox SET status='sending',attempts=attempts+1,locked_at=now(),updated_at=now() WHERE id=$1::uuid`, item.ID); err != nil {
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
	if item.UserID != nil {
		var currentEmail, currentName string
		if err := db.QueryRow(ctx, `SELECT lower(btrim(email)), btrim(full_name) FROM users WHERE id = $1::uuid`, *item.UserID).Scan(&currentEmail, &currentName); err != nil {
			return failEmail(ctx, db, item, "email owner no longer exists")
		}
		if currentEmail != strings.ToLower(strings.TrimSpace(item.RecipientEmail)) {
			return failEmail(ctx, db, item, "email recipient no longer belongs to the queued account")
		}
		item.RecipientEmail = currentEmail
		item.RecipientName = currentName
	}
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
	owner := "direct"
	if item.UserID != nil {
		owner = *item.UserID
	}
	log.Printf("email outbox delivered id=%s owner=%s template=%s recipient_hash=%s", item.ID, owner, item.TemplateType, hex.EncodeToString(recipientDigest[:8]))
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
	reason := strings.TrimSpace(message)
	if reason == "" {
		reason = "unknown SMTP delivery error"
	}
	log.Printf("email outbox delivery deferred id=%s template=%s attempt=%d status=%s reason=%q", item.ID, item.TemplateType, item.Attempts, status, reason)
	return fmt.Errorf("email delivery deferred: %s", reason)
}

func tokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
