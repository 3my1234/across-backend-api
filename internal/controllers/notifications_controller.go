package controllers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type NotificationsController struct {
	db *pgxpool.Pool
}

func NewNotificationsController(db *pgxpool.Pool) *NotificationsController {
	return &NotificationsController{db: db}
}

func (nc *NotificationsController) List(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	rows, err := nc.db.Query(c.Context(), `
		SELECT id, order_id, type, title, body, data, is_read, created_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 100
	`, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "notifications unavailable")
	}
	defer rows.Close()

	notifications := make([]fiber.Map, 0)
	for rows.Next() {
		var id string
		var orderID, dataRaw []byte
		var ntype, title, body string
		var isRead bool
		var createdAt time.Time
		if err := rows.Scan(&id, &orderID, &ntype, &title, &body, &dataRaw, &isRead, &createdAt); err != nil {
			return err
		}
		notifications = append(notifications, fiber.Map{
			"id":         id,
			"order_id":   string(orderID),
			"type":       ntype,
			"title":      title,
			"body":       body,
			"data":       string(dataRaw),
			"is_read":    isRead,
			"created_at": createdAt,
		})
	}
	return c.JSON(fiber.Map{"notifications": notifications})
}

func (nc *NotificationsController) UnreadCount(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	var count int
	err := nc.db.QueryRow(c.Context(), `
		SELECT COUNT(*)::int FROM notifications
		WHERE user_id = $1 AND is_read = false
	`, userID).Scan(&count)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "unavailable")
	}
	return c.JSON(fiber.Map{"unread_count": count})
}

func (nc *NotificationsController) MarkRead(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	notifID := c.Params("notification_id")
	_, err := nc.db.Exec(c.Context(), `
		UPDATE notifications
		SET is_read = true
		WHERE id = $1 AND user_id = $2
	`, notifID, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "notification not found")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (nc *NotificationsController) MarkAllRead(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	_, err := nc.db.Exec(c.Context(), `
		UPDATE notifications
		SET is_read = true
		WHERE user_id = $1 AND is_read = false
	`, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "update failed")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// createNotification inserts a notification for a user. Exported for use by other controllers.
func CreateNotification(ctx context.Context, db *pgxpool.Pool, userID, orderID string, batchID *string, ntype, title, body string, data map[string]any) error {
	dataJSON := []byte("{}")
	if data != nil {
		dataJSON, _ = json.Marshal(data)
	}
	_, err := db.Exec(ctx, `
		INSERT INTO notifications(user_id, order_id, batch_id, type, title, body, data)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7)
	`, userID, orderID, batchID, ntype, title, body, dataJSON)
	return err
}

// notifyBatchUsers creates notifications for all users who have orders in a batch.
func notifyBatchUsers(ctx context.Context, db *pgxpool.Pool, batchID, ntype, title, body string, data map[string]any) error {
	rows, err := db.Query(ctx, `
		SELECT DISTINCT o.user_id, o.id
		FROM orders o
		WHERE o.batch_id = $1
	`, batchID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var userID, orderID string
		if err := rows.Scan(&userID, &orderID); err != nil {
			return err
		}
		if err := CreateNotification(ctx, db, userID, orderID, &batchID, ntype, title, body, data); err != nil {
			return err
		}
	}
	return nil
}
