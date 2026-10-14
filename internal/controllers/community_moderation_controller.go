package controllers

import (
	"github.com/gofiber/fiber/v2"
	"time"
)

func (s *CommunityController) Reports(c *fiber.Ctx) error {
	rows, err := s.db.Query(c.Context(), `SELECT q.id::text,q.room_id::text,q.reason,q.state,q.created_at,u.full_name FROM chat_reports q JOIN users u ON u.id=q.reporter_user_id ORDER BY q.created_at DESC LIMIT 100`)
	if err != nil {
		return communityError(err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, reason, state, name string
		var room *string
		var at time.Time
		if err = rows.Scan(&id, &room, &reason, &state, &at, &name); err != nil {
			return communityError(err)
		}
		items = append(items, fiber.Map{"id": id, "room_id": room, "reason": reason, "state": state, "created_at": at, "reporter": name})
	}
	if rows.Err() != nil {
		return communityError(rows.Err())
	}
	return c.JSON(fiber.Map{"items": items})
}

// Moderators can inspect only conversations that have actually been reported.
// This is not a general-purpose browser of private personal messages.
func (s *CommunityController) ReportMessages(c *fiber.Ctx) error {
	id, err := communityID(c, "report_id")
	if err != nil {
		return err
	}
	rows, err := s.db.Query(c.Context(), `SELECT m.id::text,u.full_name,m.body,m.created_at,m.media_keys FROM chat_reports q JOIN chat_messages m ON m.room_id=q.room_id JOIN users u ON u.id=m.sender_user_id WHERE q.id=$1::uuid ORDER BY m.created_at DESC,m.id DESC LIMIT 100`, id)
	if err != nil {
		return communityError(err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var mid, name, body string
		var keys []string
		var at time.Time
		if err = rows.Scan(&mid, &name, &body, &at, &keys); err != nil {
			return communityError(err)
		}
		items = append(items, fiber.Map{"id": mid, "name": name, "body": body, "created_at": at, "media_urls": s.market.chatMediaURLs(keys)})
	}
	if rows.Err() != nil {
		return communityError(rows.Err())
	}
	return c.JSON(fiber.Map{"items": items})
}
func (s *CommunityController) ResolveReport(c *fiber.Ctx) error {
	id, err := communityID(c, "report_id")
	if err != nil {
		return err
	}
	result, err := s.db.Exec(c.Context(), `UPDATE chat_reports SET state='resolved' WHERE id=$1::uuid`, id)
	if err != nil {
		return communityError(err)
	}
	if result.RowsAffected() != 1 {
		return fiber.ErrNotFound
	}
	return c.SendStatus(204)
}
