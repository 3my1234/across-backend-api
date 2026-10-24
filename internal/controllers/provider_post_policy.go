package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
)

type providerPostPolicy struct {
	Posts        int    `json:"post_limit"`
	Images       int    `json:"max_images"`
	Videos       int    `json:"max_videos"`
	VideoSeconds int    `json:"max_video_seconds"`
	TownHall     bool   `json:"town_hall"`
	Visibility   string `json:"visibility"`
	Combined     bool   `json:"combined_limit"`
}

func postPolicy(tier string) providerPostPolicy {
	p := providerPostPolicy{1, 1, 0, 60, false, "limited", true}
	switch tier {
	case "bronze":
		p.Posts = 5
		p.Images = 2
		p.TownHall = true
	case "silver":
		p.Posts = 10
		p.Images = 4
		p.TownHall = true
		p.Visibility = "slightly_limited"
	case "gold":
		p.Posts = 20
		p.Images = 8
		p.Videos = 2
		p.TownHall = true
		p.Visibility = "increased"
	case "diamond":
		p.Posts = 40
		p.Images = 8
		p.Videos = 5
		p.TownHall = true
		p.Visibility = "increased"
	}
	return p
}
func (m *ProviderMarketplaceController) postPolicy(ctx context.Context, id string) (providerPostPolicy, error) {
	var tier string
	err := m.db.QueryRow(ctx, `SELECT badge_tier FROM provider_public_identity WHERE provider_id=$1::uuid`, id).Scan(&tier)
	return postPolicy(tier), err
}
func (m *ProviderMarketplaceController) ProviderPostPolicy(c *fiber.Ctx) error {
	u, _ := c.Locals("user_id").(string)
	p, _, err := m.providerForUser(c.Context(), u)
	if err != nil {
		return fiber.ErrForbidden
	}
	policy, err := m.postPolicy(c.Context(), p)
	if err != nil {
		return fiber.NewError(503, "Your plan limits could not be loaded. Please retry.")
	}
	return c.JSON(policy)
}

func mustJSONVideos(v []string) string {
	if v == nil {
		v = []string{}
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}
