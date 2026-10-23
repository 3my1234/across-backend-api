package controllers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestPushStatus reads the existing delivery ledger. Expo receipts describe
// transport acceptance, not whether the operating system displayed or sounded it.
func (nc *NotificationsController) TestPushStatus(c *fiber.Ctx) error {
	c.Set("Cache-Control", "private, no-store")
	id := c.Params("notification_id")
	if uuid.Validate(id) != nil {
		return fiber.ErrBadRequest
	}
	actor, _ := c.Locals("user_id").(string)
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	var status string
	var attempts int
	var queued, updated time.Time
	var sent, checked *time.Time
	err := nc.db.QueryRow(ctx, `SELECT d.status,d.attempts,n.created_at,d.updated_at,d.sent_at,d.receipt_checked_at
 FROM notifications n
 JOIN notification_push_deliveries d ON d.notification_id=n.id
 JOIN user_push_tokens p ON p.id=d.push_token_id
 WHERE n.id=$1::uuid AND n.user_id=$2::uuid AND p.user_id=$2::uuid
 AND n.type='sound_test' AND n.data->>'test_push_token_id'=p.id::text`, id, actor).
		Scan(&status, &attempts, &queued, &updated, &sent, &checked)
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if err != nil {
		return fiber.NewError(503, "Notification delivery status is temporarily unavailable")
	}
	// Never return push tokens, gateway ticket identifiers or raw error messages.
	return c.JSON(fiber.Map{"status": status, "attempts": attempts, "queued_at": queued,
		"updated_at": updated, "sent_at": sent, "receipt_checked_at": checked})
}
