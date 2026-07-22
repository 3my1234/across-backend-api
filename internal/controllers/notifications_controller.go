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
        SELECT id::text, COALESCE(order_id::text, ''), type, title, body, data, is_read, created_at
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
		var id, orderID, notificationType, title, body string
		var dataRaw []byte
		var isRead bool
		var createdAt time.Time
		if err := rows.Scan(&id, &orderID, &notificationType, &title, &body, &dataRaw, &isRead, &createdAt); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "notifications unavailable")
		}
		var data any = map[string]any{}
		if len(dataRaw) > 0 {
			_ = json.Unmarshal(dataRaw, &data)
		}
		notifications = append(notifications, fiber.Map{
			"id": id, "order_id": orderID, "type": notificationType,
			"title": title, "body": body, "data": data,
			"is_read": isRead, "created_at": createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "notifications unavailable")
	}
	return c.JSON(fiber.Map{"notifications": notifications})
}

func (nc *NotificationsController) UnreadCount(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	var count int
	if err := nc.db.QueryRow(c.Context(), `SELECT COUNT(*)::int FROM notifications WHERE user_id = $1 AND is_read = false`, userID).Scan(&count); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "notifications unavailable")
	}
	return c.JSON(fiber.Map{"unread_count": count})
}

func (nc *NotificationsController) MarkRead(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	tag, err := nc.db.Exec(c.Context(), `UPDATE notifications SET is_read = true WHERE id = $1 AND user_id = $2`, c.Params("notification_id"), userID)
	if err != nil || tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "notification not found")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (nc *NotificationsController) MarkAllRead(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	if _, err := nc.db.Exec(c.Context(), `UPDATE notifications SET is_read = true WHERE user_id = $1 AND is_read = false`, userID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "notification update failed")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func marshalNotificationData(data map[string]any) ([]byte, error) {
	if data == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(data)
}

func CreateNotification(ctx context.Context, db *pgxpool.Pool, userID, orderID string, batchID *string, notificationType, title, body string, data map[string]any) error {
	return CreateNotificationOnce(ctx, db, userID, orderID, batchID, notificationType, title, body, data, "")
}

func CreateNotificationOnce(ctx context.Context, db *pgxpool.Pool, userID, orderID string, batchID *string, notificationType, title, body string, data map[string]any, eventKey string) error {
	dataJSON, err := marshalNotificationData(data)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
        INSERT INTO notifications(user_id, order_id, batch_id, type, title, body, data, event_key)
        VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, NULLIF($8, ''))
        ON CONFLICT DO NOTHING
    `, userID, orderID, batchID, notificationType, title, body, dataJSON, eventKey)
	return err
}

func notifyBatchUsers(ctx context.Context, db *pgxpool.Pool, batchID, notificationType, title, body string, data map[string]any) error {
	rows, err := db.Query(ctx, `SELECT DISTINCT o.user_id, o.id FROM orders o WHERE o.batch_id = $1`, batchID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var userID, orderID string
		if err := rows.Scan(&userID, &orderID); err != nil {
			return err
		}
		if err := CreateNotification(ctx, db, userID, orderID, &batchID, notificationType, title, body, data); err != nil {
			return err
		}
	}
	return rows.Err()
}
