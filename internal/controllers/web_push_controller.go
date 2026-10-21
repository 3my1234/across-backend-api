package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/services"
	"encoding/json"
	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WebPushController struct {
	db  *pgxpool.Pool
	cfg config.Config
}

func NewWebPushController(db *pgxpool.Pool, cfg config.Config) *WebPushController {
	return &WebPushController{db, cfg}
}
func (w *WebPushController) PublicKey(c *fiber.Ctx) error {
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"enabled": services.WebPushConfigured(w.cfg), "public_key": w.cfg.WebPushPublicKey})
}
func (w *WebPushController) Register(c *fiber.Ctx) error {
	if !services.WebPushConfigured(w.cfg) {
		return fiber.NewError(503, "Provider phone notifications are not configured yet")
	}
	var s webpush.Subscription
	if c.BodyParser(&s) != nil || !services.ValidWebPushEndpoint(s.Endpoint) || !services.ValidWebPushKeys(s.Keys.P256dh, s.Keys.Auth) {
		return fiber.NewError(422, "Invalid browser notification subscription")
	}
	actor, _ := c.Locals("user_id").(string)
	tx, err := w.db.Begin(c.Context())
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer tx.Rollback(c.Context())
	// Serialize registration even when the endpoint does not have a row yet.
	_, err = tx.Exec(c.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, s.Endpoint)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	_, err = tx.Exec(c.Context(), `DELETE FROM web_push_deliveries WHERE subscription_id IN(SELECT id FROM web_push_subscriptions WHERE endpoint=$1 AND user_id<>$2::uuid)`, s.Endpoint, actor)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	_, err = tx.Exec(c.Context(), `INSERT INTO web_push_subscriptions(user_id,endpoint,p256dh,auth,vapid_public_key) VALUES($1::uuid,$2,$3,$4,$5) ON CONFLICT(endpoint) DO UPDATE SET user_id=EXCLUDED.user_id,p256dh=EXCLUDED.p256dh,auth=EXCLUDED.auth,vapid_public_key=EXCLUDED.vapid_public_key,disabled_at=NULL,updated_at=now()`, actor, s.Endpoint, s.Keys.P256dh, s.Keys.Auth, w.cfg.WebPushPublicKey)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(c.Context()); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.SendStatus(204)
}
func (w *WebPushController) Unregister(c *fiber.Ctx) error {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if json.Unmarshal(c.Body(), &req) != nil {
		return fiber.ErrBadRequest
	}
	actor, _ := c.Locals("user_id").(string)
	_, err := w.db.Exec(c.Context(), `WITH disabled AS (UPDATE web_push_subscriptions SET disabled_at=now(),updated_at=now() WHERE user_id=$1::uuid AND endpoint=$2 RETURNING id)
 UPDATE web_push_deliveries SET status='cancelled',updated_at=now() WHERE subscription_id IN(SELECT id FROM disabled) AND status IN ('pending','retry')`, actor, req.Endpoint)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.SendStatus(204)
}

// Test only the authenticated user's current browser; never accept recipients
// or message contents from the client. Repeated taps reuse the latest test.
func (w *WebPushController) Test(c *fiber.Ctx) error {
	if !services.WebPushConfigured(w.cfg) {
		return fiber.NewError(503, "Phone notifications are not configured")
	}
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if c.BodyParser(&in) != nil || !services.ValidWebPushEndpoint(in.Endpoint) {
		return fiber.ErrUnprocessableEntity
	}
	actor, _ := c.Locals("user_id").(string)
	tx, err := w.db.Begin(c.Context())
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer tx.Rollback(c.Context())
	var subscription string
	err = tx.QueryRow(c.Context(), `SELECT id::text FROM web_push_subscriptions WHERE user_id=$1::uuid AND endpoint=$2 AND disabled_at IS NULL AND vapid_public_key=$3 FOR UPDATE`, actor, in.Endpoint, w.cfg.WebPushPublicKey).Scan(&subscription)
	if err == pgx.ErrNoRows {
		return fiber.NewError(409, "Reconnect phone notifications, then retry the test")
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	var id string
	err = tx.QueryRow(c.Context(), `SELECT id::text FROM web_push_deliveries WHERE subscription_id=$1::uuid AND source_key LIKE 'browser-test:%' AND created_at>now()-interval '30 seconds' ORDER BY created_at DESC LIMIT 1`, subscription).Scan(&id)
	if err == pgx.ErrNoRows {
		id = uuid.NewString()
		_, err = tx.Exec(c.Context(), `INSERT INTO web_push_deliveries(id,subscription_id,source_key,payload) VALUES($1::uuid,$2::uuid,'browser-test:'||$1::text,jsonb_build_object('title','Atlantic Express notification test','body','If you can see this phone notification, browser push reached this device.','view','overview','tag','atl-browser-test'))`, id, subscription)
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(c.Context()); err != nil {
		return fiber.ErrInternalServerError
	}
	c.Set("Cache-Control", "private, no-store")
	return c.Status(202).JSON(fiber.Map{"id": id})
}

func (w *WebPushController) TestStatus(c *fiber.Ctx) error {
	if _, err := uuid.Parse(c.Params("delivery_id")); err != nil {
		return fiber.ErrBadRequest
	}
	actor, _ := c.Locals("user_id").(string)
	var status string
	err := w.db.QueryRow(c.Context(), `SELECT d.status FROM web_push_deliveries d JOIN web_push_subscriptions s ON s.id=d.subscription_id WHERE d.id=$1::uuid AND s.user_id=$2::uuid AND d.source_key LIKE 'browser-test:%'`, c.Params("delivery_id"), actor).Scan(&status)
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"status": status})
}
