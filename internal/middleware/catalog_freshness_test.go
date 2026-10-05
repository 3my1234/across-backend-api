package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

type revisionRow struct {
	revision int64
	err      error
}

func (r revisionRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.revision
	return nil
}
func (r revisionRow) QueryRow(context.Context, string, ...any) pgx.Row { return r }

func TestCatalogFreshnessDisallowsStaleEdgeResponses(t *testing.T) {
	for _, tc := range []struct {
		name, url, header string
		reader            revisionRow
		want              string
	}{
		{"normal", "/api/v1/products", "", revisionRow{revision: 1}, "public, no-cache, must-revalidate"},
		{"pull refresh", "/api/v1/products?fresh=123", "", revisionRow{revision: 1}, "no-store"},
		{"service refresh", "/api/v1/marketplace/listings?refresh=123", "", revisionRow{revision: 1}, "no-store"},
		{"header refresh", "/api/v1/products", "no-cache", revisionRow{revision: 1}, "no-store"},
		{"migration recovery", "/api/v1/products", "", revisionRow{err: errors.New("unavailable")}, "no-store"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			app.Use(CatalogFreshness(tc.reader))
			app.Get("/*", func(c *fiber.Ctx) error {
				c.Set(fiber.HeaderCacheControl, "public, max-age=5, stale-while-revalidate=15")
				return c.JSON(fiber.Map{"price": 100})
			})
			request := httptest.NewRequest("GET", tc.url, nil)
			request.Header.Set("Cache-Control", tc.header)
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.Header.Get("Cache-Control") != tc.want {
				t.Fatalf("cache policy=%s", response.Header.Get("Cache-Control"))
			}
		})
	}
}

func TestCatalogTokenChangesForCommittedWritesAndExpiry(t *testing.T) {
	now := time.Unix(120, 0)
	token := CatalogChangeToken(1, now)
	if token == CatalogChangeToken(2, now) {
		t.Fatal("edit did not invalidate token")
	}
	if token == CatalogChangeToken(1, now.Add(time.Minute)) {
		t.Fatal("time-based eligibility was not rechecked")
	}
}

func TestRefreshNoncesShareOnlyTheSameDeliveryVariant(t *testing.T) {
	one := canonicalPublicURL("/api/v1/products?country_code=NG&city=Abuja&fresh=1")
	two := canonicalPublicURL("/api/v1/products?refresh=2&city=Abuja&country_code=NG")
	if one != two {
		t.Fatal("fresh current responses cannot be shared")
	}
	if one == canonicalPublicURL("/api/v1/products?country_code=US&city=Abuja&refresh=2") {
		t.Fatal("different country prices share a cache entry")
	}
	if one == canonicalPublicURL("/api/v1/products?country_code=NG&city=Lagos&refresh=2") {
		t.Fatal("different city delivery offers share a cache entry")
	}
}
