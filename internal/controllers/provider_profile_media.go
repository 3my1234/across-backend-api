package controllers

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Profile media must come from this provider's dedicated public branding folder.
// In particular, a private verification document cannot become a public avatar.
func providerBrandKeyAllowed(key, providerID, purpose string) bool {
	prefix := "user-uploads/providers/" + providerID + "/" + purpose + "/"
	return strings.HasPrefix(key, prefix) && len(key) > len(prefix) && !strings.Contains(key, "..") && !strings.ContainsAny(key, "?#\\")
}

func (m *ProviderMarketplaceController) UpdateProviderMedia(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, role, err := m.providerForUser(c.Context(), userID)
	if err != nil || (role != "owner" && role != "manager") {
		return fiber.ErrForbidden
	}
	var req struct {
		Photo  *string `json:"photo_url"`
		Banner *string `json:"banner_url"`
	}
	if c.BodyParser(&req) != nil || (req.Photo == nil && req.Banner == nil) {
		return fiber.ErrBadRequest
	}
	for _, item := range []struct {
		value   *string
		purpose string
	}{{req.Photo, "logo"}, {req.Banner, "banner"}} {
		if item.value == nil {
			continue
		}
		*item.value = strings.TrimSpace(*item.value)
		if *item.value == "" {
			continue
		}
		if m.s3 == nil || !m.s3.Configured() {
			return fiber.NewError(503, "Media storage is not configured")
		}
		key := m.s3.KeyFromURL(*item.value)
		decoded, e := url.PathUnescape(key)
		if e != nil || !providerBrandKeyAllowed(decoded, providerID, item.purpose) {
			return fiber.NewError(422, "Choose an image uploaded to your own provider profile")
		}
		ctx, cancel := context.WithTimeout(c.Context(), 6*time.Second)
		headURL, e := m.s3.ObjectHeadURL(key, time.Minute)
		if e != nil {
			cancel()
			return fiber.NewError(503, "Could not verify profile image")
		}
		request, e := http.NewRequestWithContext(ctx, http.MethodHead, headURL, nil)
		if e != nil {
			cancel()
			return fiber.ErrInternalServerError
		}
		response, e := http.DefaultClient.Do(request)
		if e != nil {
			cancel()
			return fiber.NewError(502, "Could not verify the uploaded image. Please retry")
		}
		response.Body.Close()
		cancel()
		if response.StatusCode != 200 || response.ContentLength <= 0 || response.ContentLength > 5<<20 || !supportedPortableImageMime(strings.Split(response.Header.Get("Content-Type"), ";")[0]) {
			return fiber.NewError(422, "Image upload is incomplete or exceeds 5 MB")
		}
	}
	_, err = m.db.Exec(c.Context(), `UPDATE provider_organizations SET logo_url=COALESCE($2::text,logo_url),banner_url=COALESCE($3::text,banner_url),updated_at=now() WHERE id=$1::uuid`, providerID, req.Photo, req.Banner)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	return c.SendStatus(fiber.StatusNoContent)
}
