package controllers

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type businessContactQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Resolve against the target business, never the currently selected workspace.
// The owner remains an owner even when a membership row is missing or inactive.
func businessContactAccess(ctx context.Context, db businessContactQuerier, providerID, userID string) (own, manage bool, err error) {
	err = db.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM provider_organizations p WHERE p.id=$1::uuid AND (to_jsonb(p)->>'owner_user_id'=$2 OR EXISTS(SELECT 1 FROM provider_members pm WHERE pm.provider_id=p.id AND pm.user_id=$2::uuid AND pm.is_active))),
 EXISTS(SELECT 1 FROM provider_organizations p WHERE p.id=$1::uuid AND p.is_active AND EXISTS(SELECT 1 FROM provider_members pm WHERE pm.provider_id=p.id AND pm.user_id=$2::uuid AND pm.is_active AND pm.role IN ('owner','manager')))`, providerID, userID).Scan(&own, &manage)
	return
}

func independentBusinessContact(ctx context.Context, db businessContactQuerier, providerID, userID string) error {
	own, _, err := businessContactAccess(ctx, db, providerID, userID)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if own {
		return fiber.NewError(403, "You cannot message your own business. Manage your listing from the provider workspace.")
	}
	return nil
}

// Private, bounded identity check for the native contact action. It grants no
// editing permission; the existing private listing endpoints authorize edits.
func (m *ProviderMarketplaceController) BusinessContactAccess(c *fiber.Ctx) error {
	actor, _ := c.Locals("user_id").(string)
	if uuid.Validate(actor) != nil {
		return fiber.ErrUnauthorized
	}
	providerID := c.Params("provider_id")
	if uuid.Validate(providerID) != nil {
		return fiber.ErrBadRequest
	}
	own, manage, err := businessContactAccess(c.Context(), m.db, providerID, actor)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"own_business": own, "can_manage": manage})
}
