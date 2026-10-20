package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"time"
)

type MessageReceiptsController struct{ db *pgxpool.Pool }

// Inbox delivery is one bounded database write, rather than one HTTP request
// per preview. This endpoint never writes read_at and derives the actor itself.
func (s *MessageReceiptsController) BatchDelivered(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	kind, actor, err := receiptActor(c)
	if err != nil {
		return err
	}
	type entry struct {
		Channel string `json:"channel"`
		Room    string `json:"conversation_id"`
		Message string `json:"message_id"`
	}
	var in struct {
		Messages []entry `json:"messages"`
	}
	if c.BodyParser(&in) != nil || len(in.Messages) == 0 || len(in.Messages) > 100 {
		return fiber.NewError(422, "Acknowledge between 1 and 100 messages")
	}
	for _, item := range in.Messages {
		if item.Channel != "business" && item.Channel != "shared" && item.Channel != "support" {
			return fiber.ErrBadRequest
		}
		if _, err = uuid.Parse(item.Room); err != nil {
			return fiber.ErrBadRequest
		}
		if _, err = uuid.Parse(item.Message); err != nil {
			return fiber.ErrBadRequest
		}
	}
	payload, _ := json.Marshal(in.Messages)
	_, err = s.db.Exec(ctx, `WITH input AS (SELECT * FROM jsonb_to_recordset($1::jsonb) AS i(channel text,conversation_id uuid,message_id uuid)),eligible AS (
 SELECT i.* FROM input i JOIN provider_conversation_messages m ON i.channel='business' AND m.id=i.message_id AND m.conversation_id IN (`+businessThreadMembers("i.conversation_id")+`) JOIN provider_conversations c ON c.id=m.conversation_id
 WHERE $3='user' AND (c.user_id=$2::uuid OR EXISTS(SELECT 1 FROM provider_members p WHERE p.provider_id=c.provider_id AND p.user_id=$2::uuid AND p.is_active)) AND m.sender_type<>CASE WHEN c.user_id=$2::uuid THEN 'buyer' ELSE 'provider' END
 UNION ALL SELECT i.* FROM input i JOIN chat_messages m ON i.channel='shared' AND m.id=i.message_id AND m.room_id=i.conversation_id JOIN chat_participants p ON p.room_id=m.room_id AND p.user_id=$2::uuid AND p.state='active' WHERE $3='user' AND m.sender_user_id<>$2::uuid
 UNION ALL SELECT i.* FROM input i JOIN support_messages m ON i.channel='support' AND m.id=i.message_id AND m.ticket_id=i.conversation_id JOIN support_tickets t ON t.id=m.ticket_id WHERE ($3='admin' OR t.user_id=$2::uuid) AND m.sender_type<>$3
 ) INSERT INTO message_receipts(channel,conversation_id,message_id,recipient_type,recipient_id) SELECT DISTINCT channel,conversation_id,message_id,$3,$2::uuid FROM eligible ON CONFLICT(channel,message_id,recipient_type,recipient_id) DO NOTHING`, string(payload), actor, kind)
	if err != nil {
		return communityError(err)
	}
	c.Set("Cache-Control", "private, no-store")
	return c.SendStatus(204)
}

func NewMessageReceiptsController(db *pgxpool.Pool) *MessageReceiptsController {
	return &MessageReceiptsController{db}
}
func receiptActor(c *fiber.Ctx) (string, string, error) {
	kind, id := "user", c.Locals("user_id")
	if admin, ok := c.Locals("admin_id").(string); ok && admin != "" {
		kind, id = "admin", admin
	}
	value, _ := id.(string)
	if _, err := uuid.Parse(value); err != nil {
		return "", "", fiber.ErrForbidden
	}
	return kind, value, nil
}
func (s *MessageReceiptsController) Preferences(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	kind, actor, err := receiptActor(c)
	if err != nil {
		return err
	}
	enabled := true
	if err = s.db.QueryRow(ctx, `SELECT COALESCE((SELECT read_receipts_enabled FROM messaging_preferences WHERE actor_type=$1 AND actor_id=$2::uuid),true)`, kind, actor).Scan(&enabled); err != nil {
		return communityError(err)
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"read_receipts_enabled": enabled})
}
func (s *MessageReceiptsController) SavePreferences(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	kind, actor, err := receiptActor(c)
	if err != nil {
		return err
	}
	var in struct {
		Enabled *bool `json:"read_receipts_enabled"`
	}
	if c.BodyParser(&in) != nil || in.Enabled == nil {
		return fiber.NewError(422, "Choose whether read receipts are enabled")
	}
	_, err = s.db.Exec(ctx, `INSERT INTO messaging_preferences(actor_type,actor_id,read_receipts_enabled) VALUES($1,$2::uuid,$3) ON CONFLICT(actor_type,actor_id) DO UPDATE SET read_receipts_enabled=EXCLUDED.read_receipts_enabled,updated_at=now()`, kind, actor, *in.Enabled)
	if err != nil {
		return communityError(err)
	}
	return s.Preferences(c)
}

// Return static SQL only after membership authorization; no client supplies a
// sender/recipient identity. Existing read permissions also cover closed records.
func (s *MessageReceiptsController) source(ctx context.Context, channel, room, kind, actor string) (string, string, error) {
	if _, err := uuid.Parse(room); err != nil {
		return "", "", fiber.ErrBadRequest
	}
	var allowed bool
	side := ""
	var query, source string
	switch channel {
	case "business":
		if kind != "user" {
			return "", "", fiber.ErrForbidden
		}
		source = `SELECT id,sender_user_id AS sender_id,'user'::text AS sender_actor_type,sender_type FROM provider_conversation_messages WHERE conversation_id IN (` + businessThreadMembers("$1::uuid") + `) AND id=ANY($2::uuid[])`
		err := s.db.QueryRow(ctx, `SELECT CASE WHEN c.user_id=$2::uuid THEN 'buyer' ELSE 'provider' END FROM provider_conversations c WHERE c.id=$1::uuid AND (c.user_id=$2::uuid OR EXISTS(SELECT 1 FROM provider_members p WHERE p.provider_id=c.provider_id AND p.user_id=$2::uuid AND p.is_active))`, room, actor).Scan(&side)
		if err == pgx.ErrNoRows {
			return "", "", fiber.ErrForbidden
		}
		if err != nil {
			return "", "", communityError(err)
		}
		return source, side, nil
	case "shared":
		if kind != "user" {
			return "", "", fiber.ErrForbidden
		}
		side = "user"
		query = `SELECT EXISTS(SELECT 1 FROM chat_participants WHERE room_id=$1::uuid AND user_id=$2::uuid AND state='active')`
		source = `SELECT id,sender_user_id AS sender_id,'user'::text AS sender_actor_type,'user'::text AS sender_type FROM chat_messages WHERE room_id=$1::uuid AND id=ANY($2::uuid[])`
	case "support":
		side = kind
		query = `SELECT EXISTS(SELECT 1 FROM support_tickets WHERE id=$1::uuid AND (user_id=$2::uuid OR $3='admin'))`
		source = `SELECT id,sender_id,sender_type AS sender_actor_type,sender_type FROM support_messages WHERE ticket_id=$1::uuid AND id=ANY($2::uuid[])`
	default:
		return "", "", fiber.ErrBadRequest
	}
	var err error
	if channel == "support" {
		err = s.db.QueryRow(ctx, query, room, actor, kind).Scan(&allowed)
	} else {
		err = s.db.QueryRow(ctx, query, room, actor).Scan(&allowed)
	}
	if err != nil {
		return "", "", communityError(err)
	}
	if !allowed {
		return "", "", fiber.ErrForbidden
	}
	return source, side, nil
}
func receiptIDs(ids []string) error {
	if len(ids) == 0 || len(ids) > 100 {
		return fiber.NewError(422, "Acknowledge between 1 and 100 messages")
	}
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return fiber.ErrBadRequest
		}
	}
	return nil
}
func (s *MessageReceiptsController) Acknowledge(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	kind, actor, err := receiptActor(c)
	if err != nil {
		return err
	}
	var in struct {
		Channel string   `json:"channel"`
		Room    string   `json:"conversation_id"`
		IDs     []string `json:"message_ids"`
		Read    bool     `json:"read"`
	}
	if c.BodyParser(&in) != nil {
		return fiber.ErrBadRequest
	}
	if err = receiptIDs(in.IDs); err != nil {
		return err
	}
	source, side, err := s.source(ctx, in.Channel, in.Room, kind, actor)
	if err != nil {
		return err
	}
	incoming := `m.sender_type<>$6`
	if in.Channel == "shared" {
		incoming = `m.sender_id<>$4::uuid`
	}
	sql := `INSERT INTO message_receipts(channel,conversation_id,message_id,recipient_type,recipient_id,read_at)
 SELECT $3,$1::uuid,m.id,$5,$4::uuid,CASE WHEN $7 AND COALESCE((SELECT read_receipts_enabled FROM messaging_preferences WHERE actor_type=$5 AND actor_id=$4::uuid),true) THEN now() END FROM (` + source + `) m
 WHERE ` + incoming + ` AND $6::text<>''
 ON CONFLICT(channel,message_id,recipient_type,recipient_id) DO UPDATE SET read_at=COALESCE(message_receipts.read_at,EXCLUDED.read_at) WHERE message_receipts.read_at IS NULL AND EXCLUDED.read_at IS NOT NULL`
	if _, err = s.db.Exec(ctx, sql, in.Room, in.IDs, in.Channel, actor, kind, side, in.Read); err != nil {
		return communityError(err)
	}
	c.Set("Cache-Control", "private, no-store")
	return c.SendStatus(204)
}
func (s *MessageReceiptsController) Status(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	kind, actor, err := receiptActor(c)
	if err != nil {
		return err
	}
	ids := strings.Split(c.Query("message_ids"), ",")
	if err = receiptIDs(ids); err != nil {
		return err
	}
	channel, room := c.Query("channel"), c.Query("conversation_id")
	source, _, err := s.source(ctx, channel, room, kind, actor)
	if err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `SELECT m.id::text,count(r.message_id)::int,
 count(r.message_id) FILTER(WHERE r.read_at IS NOT NULL AND COALESCE(reader.read_receipts_enabled,true) AND COALESCE(own.read_receipts_enabled,true))::int
 FROM (`+source+`) m
 LEFT JOIN message_receipts r ON r.channel=$3 AND r.message_id=m.id
 LEFT JOIN messaging_preferences reader ON reader.actor_type=r.recipient_type AND reader.actor_id=r.recipient_id
 LEFT JOIN messaging_preferences own ON own.actor_type=$5 AND own.actor_id=$4::uuid
 WHERE m.sender_id=$4::uuid AND m.sender_actor_type=$5 GROUP BY m.id`, room, ids, channel, actor, kind)
	if err != nil {
		return communityError(err)
	}
	defer rows.Close()
	items := map[string]any{}
	for rows.Next() {
		var id string
		var delivered, read int
		if err = rows.Scan(&id, &delivered, &read); err != nil {
			return communityError(err)
		}
		status := "sent"
		if delivered > 0 {
			status = "delivered"
		}
		if read > 0 {
			status = "read"
		}
		items[id] = fiber.Map{"status": status, "delivered_count": delivered, "read_count": read}
	}
	if err = rows.Err(); err != nil {
		return communityError(err)
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"items": items})
}
