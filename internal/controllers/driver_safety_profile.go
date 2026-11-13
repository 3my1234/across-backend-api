package controllers

import (
	"context"
	"errors"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

// This authenticated projection exposes only reviewed matching photos, never
// licence/ID/insurance documents or raw private storage keys.
func (m *ProviderMarketplaceController) DriverSafetyProfile(c *fiber.Ctx) error {
	c.Set("Cache-Control", "private, no-store")
	if actor, _ := c.Locals("user_id").(string); actor == "" {
		return fiber.ErrUnauthorized
	}
	if _, err := uuid.Parse(c.Params("listing_id")); err != nil {
		return fiber.ErrNotFound
	}
	if m.s3 == nil || !m.s3.Configured() {
		return fiber.NewError(503, "Driver photos are temporarily unavailable")
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	var provider, name, face, vehicle, plate string
	err := m.db.QueryRow(ctx, `SELECT l.provider_id::text,u.full_name,
 (SELECT document_url FROM provider_verification_documents WHERE provider_id=l.provider_id AND document_type='driver_face' ORDER BY created_at DESC,id DESC LIMIT 1),
 (SELECT document_url FROM provider_verification_documents WHERE provider_id=l.provider_id AND document_type='driver_vehicle' ORDER BY created_at DESC,id DESC LIMIT 1),
 (SELECT document_url FROM provider_verification_documents WHERE provider_id=l.provider_id AND document_type='driver_plate' ORDER BY created_at DESC,id DESC LIMIT 1)
 FROM provider_listings l JOIN provider_public_identity i ON i.provider_id=l.provider_id
 JOIN provider_organizations p ON p.id=l.provider_id JOIN users u ON u.id=p.owner_user_id
 WHERE l.id=$1::uuid AND l.status='approved' AND l.plan_slot<=i.listing_limit AND i.verified AND i.paid_active
 AND driver_documents_approved(l.provider_id) AND `+driverListingSQL, c.Params("listing_id")).Scan(&provider, &name, &face, &vehicle, &plate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(404, "This driver's reviewed photos are unavailable. Refresh nearby drivers.")
		}
		return fiber.NewError(503, "Driver photos are temporarily unavailable")
	}
	urls := []string{face, vehicle, plate}
	for index, raw := range urls {
		key := m.s3.KeyFromURL(raw)
		if !strings.HasPrefix(key, "private/providers/"+provider+"/verification/evidence/") || strings.Contains(key, "..") {
			return fiber.NewError(503, "Driver photo verification is incomplete")
		}
		signed, err := m.s3.ObjectGetURL(key, 5*time.Minute)
		if err != nil {
			return fiber.NewError(503, "Driver photos are temporarily unavailable")
		}
		urls[index] = signed
	}
	return c.JSON(fiber.Map{"driver_name": name, "face_photo_url": urls[0], "vehicle_photo_url": urls[1], "plate_photo_url": urls[2], "expires_in": 300})
}
