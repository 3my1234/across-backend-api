package middleware

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

type CatalogRevisionReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func CatalogChangeToken(revision int64, now time.Time) string {
	// Recheck time-based eligibility even when expiry causes no database write.
	return strconv.FormatInt(revision, 10) + ":" + strconv.FormatInt(now.Unix()/60, 10)
}

func CatalogFreshness(db CatalogRevisionReader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if c.Method() != fiber.MethodGet || !publicCacheablePath(c.Path()) {
			return c.Next()
		}
		var revision int64
		if err := db.QueryRow(c.Context(), `SELECT revision FROM catalog_revision WHERE singleton=true`).Scan(&revision); err != nil {
			// During migration/recovery, never fall back to an old cache.
			c.Locals("catalog_cache_bypass", true)
		} else {
			c.Locals("catalog_revision", CatalogChangeToken(revision, time.Now()))
		}
		c.Set(fiber.HeaderCacheControl, "public, no-cache, must-revalidate")
		err := c.Next()
		if c.Locals("catalog_cache_bypass") == true || publicCacheBypass(c) || c.Response().StatusCode() != fiber.StatusOK {
			c.Set(fiber.HeaderCacheControl, "no-store")
		} else {
			c.Set(fiber.HeaderCacheControl, "public, no-cache, must-revalidate")
		}
		return err
	}
}

func publicCacheBypass(c *fiber.Ctx) bool {
	return c.Locals("catalog_cache_bypass") == true ||
		c.Query("refresh") != "" || c.Query("fresh") != "" || c.Query("_") != "" ||
		strings.Contains(strings.ToLower(c.Get(fiber.HeaderCacheControl)), "no-cache")
}
