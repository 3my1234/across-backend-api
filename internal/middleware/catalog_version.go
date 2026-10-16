package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/sync/singleflight"
)

// CatalogVersion coalesces the public change HINT, not prices or private data.
// One primary query per second/process serves all foreground catalogue watchers.
// Actual catalogue fetches keep CatalogFreshness's primary-DB revalidation.
func CatalogVersion(db CatalogRevisionReader) fiber.Handler {
	return newCatalogVersion(db, time.Now)
}

func newCatalogVersion(db CatalogRevisionReader, now func() time.Time) fiber.Handler {
	var mu sync.Mutex
	var flights singleflight.Group
	var revision int64
	var validUntil time.Time
	read := func() (int64, error) {
		value, err, _ := flights.Do("public-revision", func() (any, error) {
			mu.Lock()
			if now().Before(validUntil) {
				current := revision
				mu.Unlock()
				return current, nil
			}
			mu.Unlock()
			// Never retain a pooled Fiber request context in a shared flight.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var current int64
			if err := db.QueryRow(ctx, `SELECT revision FROM catalog_revision WHERE singleton=true`).Scan(&current); err != nil {
				return nil, err
			}
			mu.Lock()
			revision, validUntil = current, now().Add(time.Second)
			mu.Unlock()
			return current, nil
		})
		if err != nil {
			return 0, err
		}
		return value.(int64), nil
	}
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")
		current, err := read()
		if err != nil {
			c.Set("Retry-After", "2")
			return fiber.NewError(fiber.StatusServiceUnavailable, "catalog version unavailable")
		}
		return c.JSON(fiber.Map{"change_token": CatalogChangeToken(current, now())})
	}
}
