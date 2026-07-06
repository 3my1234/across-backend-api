package middleware

import (
	"strings"

	"across/backend/internal/auth"
	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
)

func RequireAuth(cfg config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		header := c.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			return fiber.NewError(fiber.StatusUnauthorized, "missing bearer token")
		}
		token := strings.TrimPrefix(header, "Bearer ")
		claims, err := auth.Verify(token, cfg.JWTSecret)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "invalid or expired token")
		}
		c.Locals("user_id", claims.UserID)
		return c.Next()
	}
}

func RequireAdmin(c *fiber.Ctx) error {
	if c.Get("X-Admin-Token") == "" {
		return fiber.NewError(fiber.StatusUnauthorized, "missing admin token")
	}
	return c.Next()
}
