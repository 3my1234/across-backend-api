package middleware

import (
	"strings"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
)

func RequireAllowedCountry(cfg config.Config) fiber.Handler {
	allowed := parseAllowedCountries(cfg.AllowedCountries)
	defaultCountry := strings.ToUpper(strings.TrimSpace(cfg.DefaultCountry))
	if len(allowed) == 0 && defaultCountry != "" {
		allowed = map[string]struct{}{defaultCountry: {}}
	}

	return func(c *fiber.Ctx) error {
		country := strings.ToUpper(strings.TrimSpace(c.Get("X-Client-Country-Code")))
		if country == "" {
			return fiber.NewError(fiber.StatusBadRequest, "client country header required")
		}
		if _, ok := allowed[country]; !ok {
			return fiber.NewError(fiber.StatusForbidden, "service currently unavailable in your country")
		}
		return c.Next()
	}
}

func parseAllowedCountries(raw string) map[string]struct{} {
	allowed := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		code := strings.ToUpper(strings.TrimSpace(part))
		if len(code) != 2 {
			continue
		}
		allowed[code] = struct{}{}
	}
	return allowed
}
