package controllers

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

type transportQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func transportPlanError(transport bool, role, code string, amount float64, months int) error {
	if !transport {
		return nil
	}
	if role == "" {
		return fiber.NewError(422, "Choose car driver or rider in Subscriptions before your next purchase. Existing paid access is unchanged.")
	}
	wanted, price := "provider-business", float64(5000)
	if role == "rider" {
		wanted, price = "provider-growth", 2500
	}
	if code != wanted || months != 1 && months != 12 {
		return fiber.NewError(422, "Choose your transport plan: car drivers NGN5,000/month or riders NGN2,500/month, with monthly or discounted annual access.")
	}
	if amount != price {
		return fiber.NewError(503, "Your transport price is being configured. Refresh billing before paying.")
	}
	return nil
}

func checkTransportPlan(ctx context.Context, q transportQuery, provider, plan string, months int) error {
	var transport bool
	var role, code string
	var amount float64
	err := q.QueryRow(ctx, `SELECT COALESCE((to_jsonb(o)->>'is_driver')::boolean,false),COALESCE(to_jsonb(o)->>'transport_role','') FROM provider_organizations o WHERE o.id=$1::uuid`, provider).Scan(&transport, &role)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if !transport {
		return nil
	}
	err = q.QueryRow(ctx, `SELECT code,amount_ngn FROM provider_subscription_plans WHERE id=$1::uuid AND is_active`, plan).Scan(&code, &amount)
	if err == pgx.ErrNoRows {
		return fiber.NewError(422, "Choose an available plan.")
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return transportPlanError(transport, role, code, amount, months)
}

// A role is selected once by the owner. Changing it later requires support,
// so an in-flight checkout cannot race a change to a cheaper transport role.
func (m *ProviderMarketplaceController) SelectTransportRole(c *fiber.Ctx) error {
	user, _ := c.Locals("user_id").(string)
	provider, role, err := m.providerForUser(c.Context(), user)
	if err != nil || role != "owner" {
		return fiber.ErrForbidden
	}
	var input struct {
		Role string `json:"transport_role"`
	}
	if c.BodyParser(&input) != nil || input.Role != "driver" && input.Role != "rider" {
		return fiber.NewError(422, "Choose car driver or rider.")
	}
	result, err := m.db.Exec(c.Context(), `UPDATE provider_organizations SET transport_role=$2 WHERE id=$1::uuid AND is_driver AND (transport_role='' OR transport_role=$2)`, provider, input.Role)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if result.RowsAffected() != 1 {
		return fiber.NewError(409, "Transport type is already set or this is not a transport profile. Contact Support for corrections.")
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"transport_role": input.Role})
}
