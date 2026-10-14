package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

// Audience checks happen before signing image URLs. A removed friendship or a
// new block takes effect on the next request, even for an unexpired status.
const statusVisibleSQL = `s.deleted_at IS NULL AND s.expires_at>now() AND
 (s.user_id=$1::uuid OR (EXISTS(SELECT 1 FROM social_connections f
 WHERE f.user_low=LEAST(s.user_id,$1::uuid) AND f.user_high=GREATEST(s.user_id,$1::uuid) AND f.state='accepted')
 AND NOT EXISTS(SELECT 1 FROM social_blocks b WHERE
 (b.blocker_id=s.user_id AND b.blocked_id=$1::uuid) OR (b.blocker_id=$1::uuid AND b.blocked_id=s.user_id))
 AND (s.audience='friends' OR EXISTS(SELECT 1 FROM social_status_recipients a WHERE a.status_id=s.id AND a.user_id=$1::uuid))))`

func (s *CommunityController) Statuses(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	if page.CursorID != "" {
		if _, err = uuid.Parse(page.CursorID); err != nil {
			return fiber.ErrBadRequest
		}
	}
	rows, err := s.db.Query(c.Context(), `SELECT s.id::text,s.user_id::text,u.full_name,COALESCE(p.handle,''),s.body,s.media_keys,s.audience,s.created_at,s.expires_at
 FROM social_statuses s JOIN users u ON u.id=s.user_id LEFT JOIN social_profiles p ON p.user_id=u.id WHERE `+statusVisibleSQL+`
 AND ($2::timestamptz IS NULL OR (s.created_at,s.id)<($2,$3::uuid)) ORDER BY s.created_at DESC,s.id DESC LIMIT $4`, communityActor(c), page.CursorTime, nullID(page.CursorID), page.Limit+1)
	if err != nil {
		return communityError(err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	next := ""
	for rows.Next() {
		var id, owner, name, handle, body, audience string
		var keys []string
		var created, expires time.Time
		if err = rows.Scan(&id, &owner, &name, &handle, &body, &keys, &audience, &created, &expires); err != nil {
			return communityError(err)
		}
		if len(items) == page.Limit {
			last := items[len(items)-1]
			next = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
			break
		}
		items = append(items, fiber.Map{"id": id, "user_id": owner, "name": name, "handle": handle, "body": body, "media_urls": s.market.chatMediaURLs(keys), "audience": audience, "created_at": created, "expires_at": expires, "is_mine": owner == communityActor(c)})
	}
	if rows.Err() != nil {
		return communityError(rows.Err())
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"items": items, "next_cursor": next})
}
func (s *CommunityController) CreateStatus(c *fiber.Ctx) error {
	input, err := s.market.parseConversationMessage(c)
	if err != nil {
		return err
	}
	if input.ClientID == "" {
		return fiber.NewError(422, "A status reference is required")
	}
	var audience struct {
		Audience   string   `json:"audience"`
		Recipients []string `json:"recipients"`
	}
	if c.BodyParser(&audience) != nil {
		return fiber.ErrBadRequest
	}
	if audience.Audience != "friends" && audience.Audience != "selected" {
		return fiber.NewError(422, "Choose friends or selected friends")
	}
	if audience.Audience == "friends" {
		audience.Recipients = nil
	}
	if len(audience.Recipients) > 100 {
		return fiber.NewError(422, "Choose up to 100 friends")
	}
	if audience.Audience == "selected" && len(audience.Recipients) == 0 {
		return fiber.NewError(422, "Choose at least one friend")
	}
	actor := communityActor(c)
	seen := map[string]bool{}
	ids := []string{}
	for _, id := range audience.Recipients {
		if _, err = uuid.Parse(id); err != nil || id == actor {
			return fiber.ErrBadRequest
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	for _, id := range ids {
		var accepted bool
		err = tx.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM social_connections WHERE user_low=LEAST($1::uuid,$2::uuid) AND user_high=GREATEST($1::uuid,$2::uuid) AND state='accepted') AND NOT EXISTS(SELECT 1 FROM social_blocks WHERE (blocker_id=$1::uuid AND blocked_id=$2::uuid) OR (blocker_id=$2::uuid AND blocked_id=$1::uuid))`, actor, id).Scan(&accepted)
		if err != nil {
			return communityError(err)
		}
		if !accepted {
			return fiber.NewError(422, "Choose accepted friends only")
		}
	}
	var id string
	var created, expires time.Time
	err = tx.QueryRow(c.Context(), `INSERT INTO social_statuses(user_id,client_id,body,media_keys,audience) VALUES($1::uuid,$2::uuid,$3,$4,$5)
 ON CONFLICT(user_id,client_id) DO UPDATE SET id=social_statuses.id WHERE social_statuses.body=EXCLUDED.body AND social_statuses.media_keys=EXCLUDED.media_keys AND social_statuses.audience=EXCLUDED.audience AND social_statuses.deleted_at IS NULL RETURNING id::text,created_at,expires_at`, actor, input.ClientID, input.Body, input.MediaKeys, audience.Audience).Scan(&id, &created, &expires)
	if err == pgx.ErrNoRows {
		return fiber.NewError(409, "Status reference already used")
	}
	if err != nil {
		return communityError(err)
	}
	// Retry must preserve the audience. Compare previously stored recipients before
	// inserting, rather than silently widening a status on a retried submission.
	var stored []string
	err = tx.QueryRow(c.Context(), `SELECT COALESCE(array_agg(user_id::text ORDER BY user_id),'{}') FROM social_status_recipients WHERE status_id=$1::uuid`, id).Scan(&stored)
	if err != nil {
		return communityError(err)
	}
	if len(stored) > 0 {
		if len(stored) != len(ids) {
			return fiber.NewError(409, "Status reference already used")
		}
		for _, old := range stored {
			if !seen[old] {
				return fiber.NewError(409, "Status reference already used")
			}
		}
	}
	for _, peer := range ids {
		_, err = tx.Exec(c.Context(), `INSERT INTO social_status_recipients(status_id,user_id) VALUES($1::uuid,$2::uuid) ON CONFLICT DO NOTHING`, id, peer)
		if err != nil {
			return communityError(err)
		}
	}
	if err = tx.Commit(c.Context()); err != nil {
		return communityError(err)
	}
	return c.Status(201).JSON(fiber.Map{"id": id, "created_at": created, "expires_at": expires})
}
func (s *CommunityController) DeleteStatus(c *fiber.Ctx) error {
	id, err := communityID(c, "status_id")
	if err != nil {
		return err
	}
	result, err := s.db.Exec(c.Context(), `UPDATE social_statuses SET deleted_at=COALESCE(deleted_at,now()) WHERE id=$2::uuid AND user_id=$1::uuid`, communityActor(c), id)
	if err != nil {
		return communityError(err)
	}
	if result.RowsAffected() != 1 {
		return fiber.ErrNotFound
	}
	return c.SendStatus(204)
}
