package controllers

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"across/backend/internal/auth"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// Serialize permission mutations and recheck the actor inside the transaction:
// an old request cannot use privileges another super admin has just revoked.
func (a *AdminController) beginAdminAccess(c *fiber.Ctx) (pgx.Tx, context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	tx, err := a.db.Begin(ctx)
	if err != nil {
		cancel()
		return nil, nil, func() {}, fiber.ErrServiceUnavailable
	}
	fail := func(e error) (pgx.Tx, context.Context, context.CancelFunc, error) {
		_ = tx.Rollback(ctx)
		cancel()
		return nil, nil, func() {}, e
	}
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='3s';SET LOCAL statement_timeout='8s'"); err != nil {
		return fail(fiber.ErrServiceUnavailable)
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('atl-admin-access-changes'))"); err != nil {
		return fail(fiber.NewError(409, "Another admin access change is running. Refresh and retry."))
	}
	actor, _ := c.Locals("admin_id").(string)
	var role string
	if err = tx.QueryRow(ctx, "SELECT role FROM admins WHERE id=$1::uuid AND is_active FOR UPDATE", actor).Scan(&role); err != nil || auth.CanonicalAdminRole(role) != "super_admin" {
		return fail(fiber.ErrForbidden)
	}
	return tx, ctx, cancel, nil
}
func auditAdminAccess(ctx context.Context, tx pgx.Tx, actor, target, action string, before, after fiber.Map) error {
	previous, _ := json.Marshal(before)
	next, _ := json.Marshal(after)
	_, err := tx.Exec(ctx, "INSERT INTO admin_access_events(actor_admin_id,target_admin_id,action,previous_access,new_access)VALUES($1::uuid,$2::uuid,$3,$4::jsonb,$5::jsonb)", actor, target, action, previous, next)
	return err
}
func (a *AdminController) CreateAdmin(c *fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		FullName string `json:"full_name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if c.BodyParser(&req) != nil {
		return fiber.ErrBadRequest
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)
	req.Role = normalizeAdminRole(req.Role)
	if req.Email == "" || req.FullName == "" || len(req.Email) > 254 || len(req.FullName) > 160 || len(req.Password) < 10 || len(req.Password) > 72 || !isValidAdminRole(req.Role) {
		return fiber.NewError(400, "Email, name, a 10-72 byte password and a valid role are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	tx, ctx, cancel, err := a.beginAdminAccess(c)
	if err != nil {
		return err
	}
	defer cancel()
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, "INSERT INTO admins(email,full_name,password_hash,role)VALUES($1,$2,$3,$4)RETURNING id", req.Email, req.FullName, string(hash), req.Role).Scan(&id); err != nil {
		return fiber.NewError(409, "An account with that email may already exist")
	}
	actor, _ := c.Locals("admin_id").(string)
	if err = auditAdminAccess(ctx, tx, actor, id, "created", fiber.Map{}, fiber.Map{"role": req.Role, "is_active": true}); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(ctx); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.Status(201).JSON(fiber.Map{"id": id, "email": req.Email, "role": req.Role})
}
func (a *AdminController) UpdateAdminAccess(c *fiber.Ctx) error {
	var req struct {
		Role   *string `json:"role"`
		Active *bool   `json:"is_active"`
	}
	if c.BodyParser(&req) != nil || req.Role == nil && req.Active == nil {
		return fiber.ErrBadRequest
	}
	role := ""
	if req.Role != nil {
		role = auth.CanonicalAdminRole(*req.Role)
		if role == "" {
			return fiber.NewError(400, "Choose a valid admin role")
		}
	}
	return a.updateAdminAccess(c, role, req.Active)
}
func (a *AdminController) updateAdminAccess(c *fiber.Ctx, role string, active *bool) error {
	target := c.Params("admin_id")
	actor, _ := c.Locals("admin_id").(string)
	if uuid.Validate(target) != nil {
		return fiber.ErrBadRequest
	}
	if target == actor {
		return fiber.NewError(409, "You cannot change your own role or disable your own account")
	}
	tx, ctx, cancel, err := a.beginAdminAccess(c)
	if err != nil {
		return err
	}
	defer cancel()
	defer tx.Rollback(ctx)
	var previousRole string
	var previousActive bool
	if err = tx.QueryRow(ctx, "SELECT role,is_active FROM admins WHERE id=$1::uuid FOR UPDATE", target).Scan(&previousRole, &previousActive); err != nil {
		if err == pgx.ErrNoRows {
			return fiber.ErrNotFound
		}
		return fiber.ErrInternalServerError
	}
	if role == "" {
		role = previousRole
	}
	enabled := previousActive
	if active != nil {
		enabled = *active
	}
	if previousRole == "super_admin" && previousActive && (role != "super_admin" || !enabled) {
		var count int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM admins WHERE role='super_admin' AND is_active").Scan(&count); err != nil {
			return fiber.ErrInternalServerError
		}
		if count <= 1 {
			return fiber.NewError(409, "At least one active Super Admin must remain")
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE admins SET role=$2,is_active=$3,updated_at=now() WHERE id=$1::uuid", target, role, enabled); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = auditAdminAccess(ctx, tx, actor, target, "access_updated", fiber.Map{"role": previousRole, "is_active": previousActive}, fiber.Map{"role": role, "is_active": enabled}); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(ctx); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"id": target, "role": role, "is_active": enabled})
}

// Retain identities/audit history; DELETE now disables access instead of erasing it.
func (a *AdminController) DeleteAdmin(c *fiber.Ctx) error {
	disabled := false
	return a.updateAdminAccess(c, "", &disabled)
}
func (a *AdminController) ResetAdminPassword(c *fiber.Ctx) error {
	var req struct {
		Password string `json:"password"`
	}
	if c.BodyParser(&req) != nil || len(req.Password) < 10 || len(req.Password) > 72 {
		return fiber.NewError(400, "Password must contain 10-72 bytes")
	}
	target := c.Params("admin_id")
	if uuid.Validate(target) != nil {
		return fiber.ErrBadRequest
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	tx, ctx, cancel, err := a.beginAdminAccess(c)
	if err != nil {
		return err
	}
	defer cancel()
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, "UPDATE admins SET password_hash=$2,updated_at=now() WHERE id=$1::uuid", target, string(hash))
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if tag.RowsAffected() == 0 {
		return fiber.ErrNotFound
	}
	actor, _ := c.Locals("admin_id").(string)
	if err = auditAdminAccess(ctx, tx, actor, target, "password_reset", fiber.Map{}, fiber.Map{"password_reset": true}); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(ctx); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.SendStatus(204)
}
func (a *AdminController) AdminAccessEvents(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := a.db.Query(c.Context(), "SELECT id,actor_admin_id::text,target_admin_id::text,action,previous_access,new_access,created_at FROM admin_access_events WHERE ($1::timestamptz IS NULL OR(created_at,id)<($1,$2::uuid))ORDER BY created_at DESC,id DESC LIMIT $3", page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer rows.Close()
	items := []fiber.Map{}
	next := ""
	for rows.Next() {
		var id, action string
		var actor, target *string
		var before, after json.RawMessage
		var at time.Time
		if rows.Scan(&id, &actor, &target, &action, &before, &after, &at) != nil {
			return fiber.ErrInternalServerError
		}
		if len(items) == page.Limit {
			last := items[len(items)-1]
			next = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
			break
		}
		items = append(items, fiber.Map{"id": id, "actor_admin_id": actor, "target_admin_id": target, "action": action, "previous_access": before, "new_access": after, "created_at": at})
	}
	if rows.Err() != nil {
		return fiber.ErrInternalServerError
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"items": items, "next_cursor": next})
}
