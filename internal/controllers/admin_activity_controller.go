package controllers

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Activity returns a bounded, role-relevant admin activity history. Read state
// is stored server-side so it survives refreshes, browsers, and devices.
func (a *AdminController) Activity(c *fiber.Ctx) error {
	adminID, _ := c.Locals("admin_id").(string)
	role, _ := c.Locals("admin_role").(string)
	limit := c.QueryInt("limit", 25)
	if limit < 1 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}

	var cursorTime *time.Time
	var cursorID string
	if raw := strings.TrimSpace(c.Query("cursor")); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid activity cursor")
		}
		var cursor adminCursor
		if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.ID == "" {
			return fiber.NewError(fiber.StatusBadRequest, "invalid activity cursor")
		}
		cursorTime, cursorID = &cursor.CreatedAt, cursor.ID
	}

	var cursorIDValue any
	if cursorTime != nil {
		cursorIDValue = cursorID
	}
	rows, err := a.db.Query(c.Context(), `
		WITH activity_events AS (
			SELECT event.id, event.batch_id, batch.batch_code, event.event_type,
				COALESCE(event.status::text, '') AS status, event.location, event.notes,
				event.created_at, event.actor_id, 'batch'::text AS source
			FROM batch_events event
			JOIN order_batches batch ON batch.id = event.batch_id
			UNION ALL
			SELECT ticket.id, NULL::uuid, 'Support · ' || ticket.subject,
				'support_ticket_created', ticket.status, 'Support',
				COALESCE(NULLIF(buyer.email, ''), 'Buyer') || ' · ' || LEFT(ticket.message, 160),
				ticket.created_at, NULL::uuid, 'support'::text
			FROM support_tickets ticket
			JOIN users buyer ON buyer.id = ticket.user_id
		)
		SELECT event.id::text, COALESCE(event.batch_id::text, ''), event.batch_code,
			event.event_type, event.status, event.location,
			event.notes, event.created_at,
			(
				(state.read_through_created_at IS NOT NULL AND
				 (event.created_at, event.id) <= (state.read_through_created_at, state.read_through_event_id))
				OR receipt.event_id IS NOT NULL
			) AS is_read
		FROM activity_events event
		LEFT JOIN admin_activity_state state ON state.admin_id = $4::uuid
		LEFT JOIN admin_activity_reads receipt
			ON receipt.admin_id = $4::uuid AND receipt.event_id = event.id
		WHERE ($1::timestamptz IS NULL OR (event.created_at, event.id) > ($1, $2::uuid))
		  AND (event.actor_id IS NULL OR event.actor_id <> $4::uuid)
		  AND `+adminActivityVisibilitySQL+`
		ORDER BY
			CASE WHEN $1::timestamptz IS NULL THEN event.created_at END DESC,
			CASE WHEN $1::timestamptz IS NULL THEN event.id END DESC,
			event.created_at ASC, event.id ASC
		LIMIT $5
	`, cursorTime, cursorIDValue, role, adminID, limit)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "admin activity unavailable")
	}
	defer rows.Close()

	events := make([]fiber.Map, 0, limit)
	latestCursor := ""
	for rows.Next() {
		var id, batchID, batchCode, eventType, status, location, notes string
		var createdAt time.Time
		var isRead bool
		if err := rows.Scan(&id, &batchID, &batchCode, &eventType, &status, &location, &notes, &createdAt, &isRead); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "admin activity unavailable")
		}
		events = append(events, fiber.Map{
			"id": id, "batch_id": batchID, "batch_code": batchCode,
			"event_type": eventType, "status": status, "location": location,
			"notes": notes, "created_at": createdAt, "is_read": isRead,
		})
		if cursorTime != nil || latestCursor == "" {
			latestCursor = encodeAdminCursor(createdAt, id)
		}
	}
	if err := rows.Err(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "admin activity unavailable")
	}
	return c.JSON(fiber.Map{"events": events, "cursor": latestCursor})
}

// MarkActivityRead records an individual read without deleting history.
func (a *AdminController) MarkActivityRead(c *fiber.Ctx) error {
	adminID, _ := c.Locals("admin_id").(string)
	role, _ := c.Locals("admin_role").(string)
	eventID := c.Params("event_id")
	result, err := a.db.Exec(c.Context(), `
		WITH activity_events AS (
			SELECT event.id, event.status::text AS status, event.actor_id, 'batch'::text AS source
			FROM batch_events event
			UNION ALL
			SELECT ticket.id, ticket.status, NULL::uuid, 'support'::text
			FROM support_tickets ticket
		)
		INSERT INTO admin_activity_reads(admin_id, event_id)
		SELECT $1::uuid, event.id
		FROM activity_events event
		WHERE event.id = $2::uuid
		  AND (event.actor_id IS NULL OR event.actor_id <> $1::uuid)
		  AND `+adminActivityVisibilitySQL+`
		ON CONFLICT (admin_id, event_id) DO UPDATE SET read_at = now()
	`, adminID, eventID, role)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not mark activity as read")
	}
	if result.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "activity not found")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// MarkAllActivityRead advances one per-admin watermark, making the operation
// constant-size even when the event history contains millions of rows.
func (a *AdminController) MarkAllActivityRead(c *fiber.Ctx) error {
	adminID, _ := c.Locals("admin_id").(string)
	role, _ := c.Locals("admin_role").(string)
	_, err := a.db.Exec(c.Context(), `
		WITH activity_events AS (
			SELECT event.id, event.status::text AS status, event.actor_id,
				event.created_at, 'batch'::text AS source
			FROM batch_events event
			UNION ALL
			SELECT ticket.id, ticket.status, NULL::uuid, ticket.created_at, 'support'::text
			FROM support_tickets ticket
		), latest AS (
			SELECT event.created_at, event.id
			FROM activity_events event
			WHERE (event.actor_id IS NULL OR event.actor_id <> $1::uuid)
			  AND `+adminActivityVisibilityMarkAllSQL+`
			ORDER BY event.created_at DESC, event.id DESC
			LIMIT 1
		)
		INSERT INTO admin_activity_state(
			admin_id, read_through_created_at, read_through_event_id, updated_at
		)
		SELECT $1::uuid, latest.created_at, latest.id, now()
		FROM latest
		ON CONFLICT (admin_id) DO UPDATE SET
			read_through_created_at = EXCLUDED.read_through_created_at,
			read_through_event_id = EXCLUDED.read_through_event_id,
			updated_at = now()
		WHERE (admin_activity_state.read_through_created_at, admin_activity_state.read_through_event_id)
			< (EXCLUDED.read_through_created_at, EXCLUDED.read_through_event_id)
	`, adminID, role)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not mark activity as read")
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// Super/Admin I audit the whole operation. Admin II only receives its inbound
// procurement handoff. Admin III receives dispatch/arrival work and buyer
// completion. The actor exclusion above prevents self-notifications.
const adminActivityVisibilitySQL = `(
	$3::text IN ('super_admin', 'catalog_admin')
	OR (event.source = 'batch' AND $3::text = 'procurement_admin' AND COALESCE(event.status::text, '') IN
		('funds_sent_to_procurement', 'procurement_acknowledged'))
	OR (event.source = 'batch' AND $3::text = 'courier_admin' AND COALESCE(event.status::text, '') IN
		('procurement_complete', 'enroute_nigeria', 'arrived_local', 'ready_for_pickup', 'completed'))
)`

const adminActivityVisibilityMarkAllSQL = `(
	$2::text IN ('super_admin', 'catalog_admin')
	OR (event.source = 'batch' AND $2::text = 'procurement_admin' AND COALESCE(event.status::text, '') IN
		('funds_sent_to_procurement', 'procurement_acknowledged'))
	OR (event.source = 'batch' AND $2::text = 'courier_admin' AND COALESCE(event.status::text, '') IN
		('procurement_complete', 'enroute_nigeria', 'arrived_local', 'ready_for_pickup', 'completed'))
)`
