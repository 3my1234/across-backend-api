package controllers

import (
	"encoding/json"
	"errors"
	"log"
	"strings"

	"across/backend/internal/config"
	"across/backend/internal/services"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SESController struct {
	db       *pgxpool.Pool
	cfg      config.Config
	verifier *services.SNSVerifier
}

func NewSESController(db *pgxpool.Pool, cfg config.Config) *SESController {
	return &SESController{db: db, cfg: cfg, verifier: services.NewSNSVerifier()}
}

type sesFeedbackEvent struct {
	NotificationType string `json:"notificationType"`
	EventType        string `json:"eventType"`
	Mail             struct {
		MessageID string `json:"messageId"`
	} `json:"mail"`
	Bounce struct {
		BounceType        string `json:"bounceType"`
		BounceSubType     string `json:"bounceSubType"`
		BouncedRecipients []struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"bouncedRecipients"`
	} `json:"bounce"`
	Complaint struct {
		ComplaintFeedbackType string `json:"complaintFeedbackType"`
		ComplainedRecipients  []struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"complainedRecipients"`
	} `json:"complaint"`
}

func (s *SESController) Webhook(c *fiber.Ctx) error {
	if strings.TrimSpace(s.cfg.SESSNSTopicARN) == "" {
		return fiber.NewError(fiber.StatusServiceUnavailable, "SES feedback webhook is not configured")
	}
	var envelope services.SNSMessage
	if err := json.Unmarshal(c.BodyRaw(), &envelope); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid SNS message")
	}
	if err := s.verifier.Verify(c.Context(), envelope, s.cfg.SESSNSTopicARN); err != nil {
		log.Printf("SES SNS signature verification failed message_id=%s: %v", envelope.MessageID, err)
		return fiber.NewError(fiber.StatusUnauthorized, "invalid SNS signature")
	}

	switch envelope.Type {
	case "SubscriptionConfirmation":
		if err := s.verifier.ConfirmSubscription(c.Context(), envelope.SubscribeURL); err != nil {
			log.Printf("SES SNS subscription confirmation failed: %v", err)
			return fiber.NewError(fiber.StatusBadGateway, "SNS subscription confirmation failed")
		}
		return c.SendStatus(fiber.StatusNoContent)
	case "UnsubscribeConfirmation":
		return c.SendStatus(fiber.StatusNoContent)
	case "Notification":
		if err := s.processNotification(c, envelope); err != nil {
			log.Printf("SES feedback processing failed message_id=%s: %v", envelope.MessageID, err)
			return fiber.NewError(fiber.StatusInternalServerError, "could not process SES feedback")
		}
		return c.SendStatus(fiber.StatusNoContent)
	default:
		return fiber.NewError(fiber.StatusBadRequest, "unsupported SNS message type")
	}
}

func (s *SESController) processNotification(c *fiber.Ctx, envelope services.SNSMessage) error {
	var event sesFeedbackEvent
	if err := json.Unmarshal([]byte(envelope.Message), &event); err != nil {
		return err
	}
	eventType := strings.ToLower(strings.TrimSpace(event.NotificationType))
	if eventType == "" {
		eventType = strings.ToLower(strings.TrimSpace(event.EventType))
	}
	if eventType == "" {
		return errors.New("SES feedback event type is missing")
	}
	payload := json.RawMessage(envelope.Message)
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	result, err := tx.Exec(c.Context(), `
		INSERT INTO email_webhook_events(message_id, event_type, payload)
		VALUES ($1, $2, $3::jsonb)
		ON CONFLICT (message_id) DO NOTHING
	`, envelope.MessageID, eventType, payload)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return tx.Commit(c.Context())
	}

	type suppression struct{ email, reason string }
	suppressions := make([]suppression, 0)
	if eventType == "complaint" {
		for _, recipient := range event.Complaint.ComplainedRecipients {
			suppressions = append(suppressions, suppression{recipient.EmailAddress, "complaint"})
		}
	} else if eventType == "bounce" && strings.EqualFold(event.Bounce.BounceType, "Permanent") {
		for _, recipient := range event.Bounce.BouncedRecipients {
			suppressions = append(suppressions, suppression{recipient.EmailAddress, "permanent_bounce"})
		}
	}
	for _, item := range suppressions {
		email := strings.ToLower(strings.TrimSpace(item.email))
		if email == "" {
			continue
		}
		if _, err := tx.Exec(c.Context(), `
			INSERT INTO email_suppressions(email, reason, source_event_id, details)
			VALUES ($1, $2, $3, $4::jsonb)
			ON CONFLICT (email) DO UPDATE SET
				reason = CASE WHEN email_suppressions.reason = 'complaint' THEN email_suppressions.reason ELSE EXCLUDED.reason END,
				source_event_id = EXCLUDED.source_event_id,
				details = EXCLUDED.details,
				updated_at = now()
		`, email, item.reason, envelope.MessageID, payload); err != nil {
			return err
		}
		if _, err := tx.Exec(c.Context(), `
			UPDATE email_outbox
			SET status = 'suppressed', locked_at = NULL,
				last_error = $2, updated_at = now()
			WHERE recipient_email = $1 AND status IN ('pending', 'retry')
		`, email, "recipient suppressed after SES "+item.reason); err != nil {
			return err
		}
	}
	return tx.Commit(c.Context())
}
