package controllers

import (
	"context"
	"encoding/json"

	"github.com/gofiber/fiber/v2"
)

type providerPostPolicy struct {
	Rooms           int    `json:"town_hall_room_limit"`
	Weight          int    `json:"promotional_weight"`
	DirectMessaging bool   `json:"direct_messaging"`
	Recommended     bool   `json:"recommended"`
	Search          bool   `json:"town_hall_search"`
	Posts           int    `json:"post_limit"`
	Images          int    `json:"max_images"`
	Videos          int    `json:"max_videos"`
	VideoSeconds    int    `json:"max_video_seconds"`
	TownHall        bool   `json:"town_hall"`
	Visibility      string `json:"visibility"`
	Combined        bool   `json:"combined_limit"`
}

func postPolicy(tier string) providerPostPolicy {
	p := providerPostPolicy{Posts: 1, Images: 1, Weight: 1, VideoSeconds: 60, Visibility: "limited", Combined: true, DirectMessaging: true, Recommended: true}
	switch tier {
	case "bronze":
		p.Posts = 3
		p.Images = 3
		p.Rooms = 3
		p.Weight = 1
		p.Visibility = "basic"
	case "silver":
		p.Posts = 10
		p.Images = 5
		p.Rooms = 5
		p.Weight = 2
		p.Visibility = "standard"
	case "diamond":
		p.Posts = 25
		p.Images = 8
		p.Rooms = 8
		p.Weight = 3
		p.Videos = 2
		p.Visibility = "enhanced"
	case "gold":
		p.Posts = 60
		p.Images = 12
		p.Rooms = 10
		p.Weight = 5
		p.Videos = 5
		p.Visibility = "premium"
	}
	p.TownHall = p.Rooms > 0
	p.Search = p.TownHall
	return p
}
func (m *ProviderMarketplaceController) postPolicy(ctx context.Context, id string) (providerPostPolicy, error) {
	var raw []byte
	err := m.db.QueryRow(ctx, `SELECT provider_current_benefits($1::uuid)`, id).Scan(&raw)
	var policy providerPostPolicy
	if err == nil {
		err = json.Unmarshal(raw, &policy)
	}
	return policy, err
}
func (m *ProviderMarketplaceController) ProviderPostPolicy(c *fiber.Ctx) error {
	c.Set("Cache-Control", "private, no-store")
	u, _ := c.Locals("user_id").(string)
	p, _, err := m.providerForUser(c.Context(), u)
	if err != nil {
		return fiber.ErrForbidden
	}
	policy, err := m.postPolicy(c.Context(), p)
	if err != nil {
		return fiber.NewError(503, "Your plan limits could not be loaded. Please retry.")
	}
	usage, err := m.providerEntitlements(c.Context(), p)
	if err != nil {
		return fiber.NewError(503, "Your plan usage could not be loaded. Please retry.")
	}
	raw, _ := json.Marshal(policy)
	var result fiber.Map
	_ = json.Unmarshal(raw, &result)
	result["usage"] = usage
	result["entitlements_version"] = 1
	return c.JSON(result)
}

func mustJSONVideos(v []string) string {
	if v == nil {
		v = []string{}
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// Custom legacy plans keep their configured combined allowance.
func planPostPolicy(code string, limit int) providerPostPolicy {
	p := postPolicy(providerPlanTier(code))
	if providerPlanTier(code) == "" && limit > 0 {
		p.Posts = limit
	}
	return p
}
