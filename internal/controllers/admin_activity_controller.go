package controllers

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Activity exposes a bounded event stream for lightweight admin change
// detection. Clients poll this small indexed endpoint and reload their current
// view only when a relevant event appears.
func (a *AdminController) Activity(c *fiber.Ctx) error {
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
		SELECT event.id::text, event.batch_id::text, batch.batch_code,
			event.event_type, COALESCE(event.status::text, ''), event.location,
			event.notes, event.created_at
		FROM batch_events event
		JOIN order_batches batch ON batch.id = event.batch_id
		WHERE ($1::timestamptz IS NULL OR (event.created_at, event.id) > ($1, $2::uuid))
		  AND (
			$3::text IN ('super_admin', 'catalog_admin')
			OR ($3::text = 'procurement_admin' AND COALESCE(event.status::text, '') IN
				('funds_sent_to_procurement', 'procurement_acknowledged', 'purchasing', 'procurement_complete', 'enroute_nigeria'))
			OR ($3::text = 'courier_admin' AND COALESCE(event.status::text, '') IN
				('procurement_complete', 'enroute_nigeria', 'arrived_local', 'ready_for_pickup', 'completed'))
		  )
		ORDER BY
			CASE WHEN $1::timestamptz IS NULL THEN event.created_at END DESC,
			CASE WHEN $1::timestamptz IS NULL THEN event.id END DESC,
			event.created_at ASC, event.id ASC
		LIMIT $4
	`, cursorTime, cursorIDValue, role, limit)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "admin activity unavailable")
	}
	defer rows.Close()

	events := make([]fiber.Map, 0, limit)
	latestCursor := ""
	for rows.Next() {
		var id, batchID, batchCode, eventType, status, location, notes string
		var createdAt time.Time
		if err := rows.Scan(&id, &batchID, &batchCode, &eventType, &status, &location, &notes, &createdAt); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "admin activity unavailable")
		}
		events = append(events, fiber.Map{
			"id": id, "batch_id": batchID, "batch_code": batchCode,
			"event_type": eventType, "status": status, "location": location,
			"notes": notes, "created_at": createdAt,
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
