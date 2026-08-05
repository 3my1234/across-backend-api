package controllers

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

var expoPushTokenPattern = regexp.MustCompile(`^(ExponentPushToken|ExpoPushToken)\[[A-Za-z0-9_-]+\]$`)

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

func (nc *NotificationsController) Activity(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	var changedAt time.Time
	if err := nc.db.QueryRow(c.Context(), `
		SELECT GREATEST(
			COALESCE((SELECT updated_at FROM orders WHERE user_id = $1::uuid ORDER BY updated_at DESC, id DESC LIMIT 1), 'epoch'::timestamptz),
			COALESCE((SELECT created_at FROM notifications WHERE user_id = $1::uuid ORDER BY created_at DESC, id DESC LIMIT 1), 'epoch'::timestamptz)
		)
	`, userID).Scan(&changedAt); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "activity unavailable")
	}
	return c.JSON(fiber.Map{"change_token": changedAt.UTC().Format(time.RFC3339Nano)})
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

func (nc *NotificationsController) RegisterPushToken(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	var req struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid push token payload")
	}
	req.Token = strings.TrimSpace(req.Token)
	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	if !expoPushTokenPattern.MatchString(req.Token) || (req.Platform != "android" && req.Platform != "ios") {
		return fiber.NewError(fiber.StatusBadRequest, "invalid push token")
	}

	tx, err := nc.db.Begin(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "push registration unavailable")
	}
	defer tx.Rollback(c.Context())
	var tokenID string
	if err := tx.QueryRow(c.Context(), `
		INSERT INTO user_push_tokens(user_id, expo_push_token, platform)
		VALUES ($1::uuid, $2::text, $3::text)
		ON CONFLICT (expo_push_token) DO UPDATE
		SET user_id = EXCLUDED.user_id,
			platform = EXCLUDED.platform,
			disabled_at = NULL,
			last_seen_at = now(),
			updated_at = now()
		RETURNING id::text
	`, userID, req.Token, req.Platform).Scan(&tokenID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "push registration failed")
	}
	// Backfill only recent unread notifications. This covers the race between
	// authentication and token registration without replaying old history.
	if _, err := tx.Exec(c.Context(), `
		INSERT INTO notification_push_deliveries(notification_id, push_token_id)
		SELECT n.id, $2::uuid
		FROM notifications n
		WHERE n.user_id = $1::uuid
		  AND n.is_read = false
		  AND n.created_at >= now() - interval '24 hours'
		ORDER BY n.created_at DESC
		LIMIT 100
		ON CONFLICT DO NOTHING
	`, userID, tokenID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "push registration failed")
	}
	if err := tx.Commit(c.Context()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "push registration failed")
	}
	return c.JSON(fiber.Map{"registered": true})
}

func (nc *NotificationsController) UnregisterPushToken(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	var req struct {
		Token string `json:"token"`
	}
	if err := c.BodyParser(&req); err != nil || strings.TrimSpace(req.Token) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "invalid push token payload")
	}
	if _, err := nc.db.Exec(c.Context(), `
		UPDATE user_push_tokens
		SET disabled_at = now(), updated_at = now()
		WHERE user_id = $1::uuid AND expo_push_token = $2::text
	`, userID, strings.TrimSpace(req.Token)); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "push token update failed")
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
	_, err = db.Exec(ctx, insertNotificationSQL,
		userID, orderID, batchID, notificationType, title, body, dataJSON, eventKey)
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
