package controllers

import (
	"context"
	"errors"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

var errSignupClosed = errors.New("signup is closed")

// Caller holds the policy row share lock through account creation.
func signupAllowedTx(ctx context.Context, tx pgx.Tx, email, name string) (bool, error) {
	var mode string
	if err := tx.QueryRow(ctx, `SELECT mode FROM signup_policy WHERE singleton=true FOR SHARE`).Scan(&mode); err != nil {
		return false, err
	}
	if mode == "open" {
		return true, nil
	}
	if mode == "closed" {
		return false, errSignupClosed
	}
	if mode == "approved_only" {
		var approved bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM launch_waitlist WHERE email=$1 AND approved=true) OR EXISTS(SELECT 1 FROM signup_approved_emails WHERE email=$1)`, email).Scan(&approved); err != nil {
			return false, err
		}
		if approved {
			return true, nil
		}
	}
	return false, nil
}
func signupWaitlistError() error {
	return fiber.NewError(403, "ATL Xpres is opening in waves. Use the email you used on the waitlist, join at https://atlxpres.com/#waitlist, or contact support@atlxpres.com.")
}

// Super-admin routes are registered behind the existing role middleware.
func (w *WaitlistController) AdminSignupMode(c *fiber.Ctx) error {
	var mode string
	if err := w.db.QueryRow(c.Context(), `SELECT mode FROM signup_policy WHERE singleton=true`).Scan(&mode); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"mode": mode})
}
func (w *WaitlistController) AdminSetSignupMode(c *fiber.Ctx) error {
	var in struct {
		Mode string `json:"mode"`
	}
	if c.BodyParser(&in) != nil || (in.Mode != "open" && in.Mode != "approved_only" && in.Mode != "closed") {
		return fiber.NewError(422, "Choose open, approved_only or closed.")
	}
	tx, err := w.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	if _, err = tx.Exec(c.Context(), `UPDATE signup_policy SET mode=$1,updated_at=now() WHERE singleton=true`, in.Mode); err != nil {
		return err
	}
	if _, err = tx.Exec(c.Context(), `INSERT INTO admin_audit_logs(actor_id,action,entity_type,metadata) VALUES($1::uuid,'signup_mode_changed','signup_policy',jsonb_build_object('mode',$2::text))`, c.Locals("admin_id"), in.Mode); err != nil {
		return err
	}
	if err = tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"mode": in.Mode})
}
func (w *WaitlistController) AdminApproveWaitlist(c *fiber.Ctx) error {
	var in struct {
		Count int `json:"count"`
	}
	if c.BodyParser(&in) != nil || in.Count < 1 || in.Count > 1000 {
		return fiber.NewError(422, "Choose a number between 1 and 1000.")
	}
	tx, err := w.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	var count int
	if err = tx.QueryRow(c.Context(), `WITH picked AS (SELECT id FROM launch_waitlist WHERE approved=false ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT $1), changed AS (UPDATE launch_waitlist SET approved=true WHERE id IN (SELECT id FROM picked) RETURNING id) SELECT count(*) FROM changed`, in.Count).Scan(&count); err != nil {
		return err
	}
	if _, err = tx.Exec(c.Context(), `INSERT INTO admin_audit_logs(actor_id,action,entity_type,metadata) VALUES($1::uuid,'waitlist_batch_approved','launch_waitlist',jsonb_build_object('count',$2::int))`, c.Locals("admin_id"), count); err != nil {
		return err
	}
	if err = tx.Commit(c.Context()); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"approved": count})
}
