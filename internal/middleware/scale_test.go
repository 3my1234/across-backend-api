package middleware

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestPublicCacheablePathExcludesUserSpecificData(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/v1/products", true},
		{"/api/v1/products/flash-sale", true},
		{"/api/v1/products/9d30d2cd-6a33-4e19-b54e-f5c888f932ba", true},
		{"/api/v1/products/9d30d2cd-6a33-4e19-b54e-f5c888f932ba/recommendations", true},
		{"/api/v1/products/9d30d2cd-6a33-4e19-b54e-f5c888f932ba/reviews", false},
		{"/api/v1/products/9d30d2cd-6a33-4e19-b54e-f5c888f932ba/reviews/mine", false},
		{"/api/v1/marketplace/nearby", true},
		{"/api/v1/marketplace/listings/9d30d2cd-6a33-4e19-b54e-f5c888f932ba", true},
		{"/api/v1/marketplace/listings/9d30d2cd-6a33-4e19-b54e-f5c888f932ba/availability", false},
		{"/api/v1/orders", false},
	}
	for _, test := range tests {
		if got := publicCacheablePath(test.path); got != test.want {
			t.Fatalf("publicCacheablePath(%q) = %t, want %t", test.path, got, test.want)
		}
	}
}

func TestScaleClientIPIgnoresUnverifiedForwardingHeaders(t *testing.T) {
	app := fiber.New()
	app.Use(TrustedClientIP(""))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString(scaleClientIP(c)) })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("CF-Connecting-IP", "203.0.113.15")
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	req.Header.Set("X-Real-IP", "192.0.2.25")
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	if string(body) == "203.0.113.15" || string(body) == "198.51.100.20" || string(body) == "192.0.2.25" || len(body) == 0 {
		t.Fatalf("client IP = %q", body)
	}
}
