package controllers

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

const (
	defaultAdminPageSize = 25
	maxAdminPageSize     = 100
)

type adminPageRequest struct {
	Limit      int
	Search     string
	CursorTime *time.Time
	CursorID   string
}

type adminCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func parseAdminPage(c *fiber.Ctx) (adminPageRequest, error) {
	limit := c.QueryInt("limit", defaultAdminPageSize)
	if limit < 1 {
		limit = defaultAdminPageSize
	}
	if limit > maxAdminPageSize {
		limit = maxAdminPageSize
	}

	page := adminPageRequest{
		Limit:  limit,
		Search: strings.TrimSpace(c.Query("search")),
	}
	if len(page.Search) > 120 {
		return page, fiber.NewError(fiber.StatusBadRequest, "search is too long")
	}

	rawCursor := strings.TrimSpace(c.Query("cursor"))
	if rawCursor == "" {
		return page, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(rawCursor)
	if err != nil {
		return page, fiber.NewError(fiber.StatusBadRequest, "invalid cursor")
	}
	var cursor adminCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.ID == "" || cursor.CreatedAt.IsZero() {
		return page, fiber.NewError(fiber.StatusBadRequest, "invalid cursor")
	}
	page.CursorTime = &cursor.CreatedAt
	page.CursorID = cursor.ID
	return page, nil
}

func encodeAdminCursor(createdAt time.Time, id string) string {
	raw, _ := json.Marshal(adminCursor{CreatedAt: createdAt.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func adminPageMeta(page adminPageRequest, total int64, returned int, nextCursor string) fiber.Map {
	meta := fiber.Map{
		"limit":       page.Limit,
		"returned":    returned,
		"has_more":    nextCursor != "",
		"next_cursor": nextCursor,
	}
	// Exact totals are returned on the first page only. Re-counting millions of
	// matching rows for every subsequent cursor would make "load more" slower
	// without adding information the client does not already have.
	if page.CursorTime == nil {
		meta["total"] = total
	}
	return meta
}
