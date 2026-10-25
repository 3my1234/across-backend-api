package controllers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func providerTierLabel(tier string) string {
	if tier == "" {
		return "Free"
	}
	return strings.ToUpper(tier[:1]) + tier[1:]
}

func (m *ProviderMarketplaceController) providerEntitlements(ctx context.Context, id string) (fiber.Map, error) {
	var raw []byte
	var tier string
	var paid, verified bool
	var posts, rooms int
	err := m.db.QueryRow(ctx, `SELECT provider_current_benefits(i.provider_id),i.badge_tier,i.paid_active,i.verified,
 (SELECT count(*) FROM products WHERE provider_id=i.provider_id AND moderation_status<>'archived')+(SELECT count(*) FROM provider_listings WHERE provider_id=i.provider_id AND status<>'archived'),
 (SELECT count(DISTINCT p.room_id) FROM chat_participants p JOIN chat_rooms r ON r.id=p.room_id JOIN town_hall_requests h ON h.room_id=r.id WHERE p.provider_id=i.provider_id AND p.role='provider' AND p.state='active' AND r.kind='town_hall' AND r.state='open' AND h.engagement_deadline>clock_timestamp())
 FROM provider_public_identity i WHERE i.provider_id=$1::uuid`, id).Scan(&raw, &tier, &paid, &verified, &posts, &rooms)
	if err != nil {
		return nil, err
	}
	var benefits providerPostPolicy
	if err = json.Unmarshal(raw, &benefits); err != nil {
		return nil, err
	}
	remaining := func(limit, used int) int {
		if used >= limit {
			return 0
		}
		return limit - used
	}
	return fiber.Map{"entitlements_version": 1, "provider_id": id, "tier": tier, "tier_label": providerTierLabel(tier), "paid_active": paid, "verified": verified, "requires_verification": true, "benefits": benefits, "active_listings_used": posts, "remaining_active_listings": remaining(benefits.Posts, posts), "town_hall_rooms_used": rooms, "remaining_town_hall_rooms": remaining(benefits.Rooms, rooms), "listing_over_limit": posts > benefits.Posts, "room_over_limit": rooms > benefits.Rooms, "capacity_note": "Products and services share one allowance. Unarchived drafts reserve a place. Existing listings, images and joined rooms are kept after a downgrade; new listings and joins must fit your plan."}, nil
}
func (m *ProviderMarketplaceController) ProviderEntitlements(c *fiber.Ctx) error {
	actor, _ := c.Locals("user_id").(string)
	provider, _, err := m.providerForUser(c.Context(), actor)
	if err != nil {
		return fiber.ErrForbidden
	}
	result, err := m.providerEntitlements(c.Context(), provider)
	if err != nil {
		return fiber.NewError(503, "Your plan benefits could not be checked. Please retry.")
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(result)
}
