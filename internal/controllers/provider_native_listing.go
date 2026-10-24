package controllers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetMyListing returns the complete editable snapshot only to an active owning
// provider's owner/manager. Public discovery never gains private contacts.
func (m *ProviderMarketplaceController) GetMyListing(c *fiber.Ctx) error {
	c.Set("Cache-Control", "private, no-store")
	if uuid.Validate(c.Params("listing_id")) != nil {
		return fiber.ErrBadRequest
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	actor, _ := c.Locals("user_id").(string)
	provider, role, err := m.providerForUser(ctx, actor)
	if err != nil || (role != "owner" && role != "manager") {
		return fiber.ErrForbidden
	}
	var raw []byte
	err = m.db.QueryRow(ctx, `SELECT jsonb_build_object(
 'id',id,'listing_type',listing_type,'title',title,'description',description,
 'category',category,'contact_email',contact_email,'contact_phone',contact_phone,
 'address_line',address_line,'city',city,'state',state,'country_code',country_code,
 'price',price,'currency_code',currency_code,'pricing_unit',pricing_unit,
 'capacity',capacity,'media_urls',media_urls,'attributes',attributes,
 'latitude',latitude,'longitude',longitude,'service_radius_km',service_radius_km,
 'is_mobile_service',is_mobile_service,'is_available_now',is_available_now,
 'status',status,'moderation_notes',moderation_notes)
 FROM provider_listings WHERE id=$1::uuid AND provider_id=$2::uuid`, c.Params("listing_id"), provider).Scan(&raw)
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if err != nil {
		return fiber.ErrInternalServerError
	}
	var item map[string]any
	if json.Unmarshal(raw, &item) != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(item)
}
