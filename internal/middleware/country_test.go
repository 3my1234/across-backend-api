package middleware

import (
	"net/http/httptest"
	"testing"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
)

func TestNormalizedCountryCode(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{value: "ng", want: "NG"},
		{value: " NG ", want: "NG"},
		{value: "XX", want: ""},
		{value: "T1", want: ""},
		{value: "Nigeria", want: ""},
		{value: "", want: ""},
	}
	for _, test := range tests {
		if got := normalizedCountryCode(test.value); got != test.want {
			t.Fatalf("normalizedCountryCode(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestAllowedCountryUsesCloudflareCountryBeforeClientHint(t *testing.T) {
	app := fiber.New()
	app.Get("/", RequireAllowedCountry(config.Config{
		AllowedCountries: "NG",
		DefaultCountry:   "NG",
	}), func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("CF-IPCountry", "NG")
	req.Header.Set("X-Client-Country-Code", "US")
	response, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if response.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusNoContent)
	}
}

func TestAllowedCountryRejectsExplicitCloudflareCountry(t *testing.T) {
	app := fiber.New()
	app.Get("/", RequireAllowedCountry(config.Config{
		AllowedCountries: "NG",
		DefaultCountry:   "NG",
	}), func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("CF-IPCountry", "US")
	req.Header.Set("X-Client-Country-Code", "NG")
	response, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if response.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusForbidden)
	}
}
