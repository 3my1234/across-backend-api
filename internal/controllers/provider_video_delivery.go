package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"strings"
	"time"
)

// Never proxy the video body through API CPU or the default image CDN. A stable
// public link resolves only registered provider assets to a short-lived S3 URL.
func (m *ProviderMarketplaceController) PublicProviderVideo(c *fiber.Ctx) error {
	key := c.Params("*")
	parts := strings.Split(key, "/")
	if len(parts) != 5 || parts[0] != "user-uploads" || parts[1] != "providers" || parts[3] != "video" || uuid.Validate(parts[2]) != nil || !strings.HasSuffix(parts[4], ".mp4") || uuid.Validate(strings.TrimSuffix(parts[4], ".mp4")) != nil {
		return fiber.ErrNotFound
	}
	if m.s3 == nil || !m.s3.Configured() {
		return fiber.ErrServiceUnavailable
	}
	public, err := m.s3.VideoObjectURL(key)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	var registered bool
	if err = m.db.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM provider_post_video_assets WHERE url=$1 AND provider_id=$2::uuid)`, public, parts[2]).Scan(&registered); err != nil {
		return fiber.ErrServiceUnavailable
	}
	if !registered {
		return fiber.ErrNotFound
	}
	signed, err := m.s3.ObjectGetURL(key, 5*time.Minute)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	c.Set("Cache-Control", "private, no-store")
	c.Set("Referrer-Policy", "no-referrer")
	return c.Redirect(signed, fiber.StatusFound)
}
