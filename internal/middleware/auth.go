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

func RequireAdmin(cfg config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		header := c.Get("Authorization")
		if strings.HasPrefix(header, "Bearer ") {
			token := strings.TrimPrefix(header, "Bearer ")
			claims, err := auth.Verify(token, cfg.JWTSecret)
			if err == nil {
				c.Locals("admin_id", claims.UserID)
				return c.Next()
			}
		}

		adminToken := strings.TrimSpace(c.Get("X-Admin-Token"))
		if adminToken != "" && cfg.AdminBootstrapToken != "" && adminToken == cfg.AdminBootstrapToken {
			c.Locals("admin_id", "bootstrap")
			return c.Next()
		}
		return fiber.NewError(fiber.StatusUnauthorized, "missing admin token")
	}
}
