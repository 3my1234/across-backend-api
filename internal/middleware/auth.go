package middleware

import (
	"strings"

	"across/backend/internal/auth"
	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
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

func RequireAdmin(cfg config.Config, db *pgxpool.Pool) fiber.Handler {
	return RequireAdminRoles(cfg, db)
}

func RequireAdminRoles(cfg config.Config, db *pgxpool.Pool, allowedRoles ...string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		adminID, adminRole, err := authenticateAdmin(c, cfg, db)
		if err != nil {
			return err
		}
		if len(allowedRoles) > 0 && !adminRoleAllowed(adminRole, allowedRoles...) {
			return fiber.NewError(fiber.StatusForbidden, "insufficient admin role")
		}
		c.Locals("admin_id", adminID)
		c.Locals("admin_role", adminRole)
		return c.Next()
	}
}

func authenticateAdmin(c *fiber.Ctx, cfg config.Config, db *pgxpool.Pool) (string, string, error) {
	header := c.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		token := strings.TrimPrefix(header, "Bearer ")
		claims, err := auth.Verify(token, cfg.JWTSecret)
		if err != nil {
			return "", "", fiber.NewError(fiber.StatusUnauthorized, "invalid or expired token")
		}
		var role string
		if err := db.QueryRow(c.Context(), `SELECT role FROM admins WHERE id = $1 AND is_active = true`, claims.UserID).Scan(&role); err != nil {
			return "", "", fiber.NewError(fiber.StatusUnauthorized, "admin account unavailable")
		}
		return claims.UserID, role, nil
	}

	adminToken := strings.TrimSpace(c.Get("X-Admin-Token"))
	if adminToken != "" && cfg.AdminBootstrapToken != "" && adminToken == cfg.AdminBootstrapToken {
		return "bootstrap", "super_admin", nil
	}
	return "", "", fiber.NewError(fiber.StatusUnauthorized, "missing admin token")
}

func adminRoleAllowed(role string, allowedRoles ...string) bool {
	role = canonicalAdminRole(role)
	if role == "super_admin" {
		return true
	}
	for _, allowed := range allowedRoles {
		if role == canonicalAdminRole(allowed) {
			return true
		}
	}
	return false
}

func canonicalAdminRole(role string) string {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "admin":
		return "catalog_admin"
	default:
		return strings.TrimSpace(strings.ToLower(role))
	}
}
