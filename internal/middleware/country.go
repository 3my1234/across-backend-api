package middleware

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
)

type countryLookupResult struct {
	code      string
	expiresAt time.Time
}

var countryLookupCache sync.Map

func RequireAllowedCountry(cfg config.Config) fiber.Handler {
	allowed := parseAllowedCountries(cfg.AllowedCountries)
	defaultCountry := strings.ToUpper(strings.TrimSpace(cfg.DefaultCountry))
	if len(allowed) == 0 && defaultCountry != "" {
		allowed = map[string]struct{}{defaultCountry: {}}
	}

	return func(c *fiber.Ctx) error {
		country, err := detectCountryCode(c)
		if err != nil {
			return err
		}
		if country == "" {
			country = strings.ToUpper(strings.TrimSpace(c.Get("X-Client-Country-Code")))
		}
		if country == "" {
			country = defaultCountry
		}
		if country == "" {
			return fiber.NewError(fiber.StatusBadRequest, "client country unavailable")
		}
		if _, ok := allowed[country]; !ok {
			return fiber.NewError(fiber.StatusForbidden, "service currently unavailable in your country")
		}
		return c.Next()
	}
}

func detectCountryCode(c *fiber.Ctx) (string, error) {
	clientIP := clientIPFromRequest(c)
	if clientIP == "" {
		return "", nil
	}
	if parsed, err := netip.ParseAddr(clientIP); err == nil {
		if !parsed.IsValid() || !parsed.IsGlobalUnicast() {
			return "", nil
		}
	}

	if cached, ok := countryLookupCache.Load(clientIP); ok {
		entry := cached.(countryLookupResult)
		if time.Now().Before(entry.expiresAt) {
			return entry.code, nil
		}
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("https://ipapi.co/%s/country/", clientIP), nil)
	if err != nil {
		return "", nil
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil
	}
	code := strings.ToUpper(strings.TrimSpace(string(body)))
	if len(code) != 2 {
		return "", nil
	}
	countryLookupCache.Store(clientIP, countryLookupResult{code: code, expiresAt: time.Now().Add(30 * time.Minute)})
	return code, nil
}

func clientIPFromRequest(c *fiber.Ctx) string {
	forwarded := strings.TrimSpace(c.Get("X-Forwarded-For"))
	if forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			if ip := strings.TrimSpace(parts[0]); ip != "" {
				return ip
			}
		}
	}
	if ip := strings.TrimSpace(c.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if ip := strings.TrimSpace(c.Get("X-Real-IP")); ip != "" {
		return ip
	}
	return strings.TrimSpace(c.IP())
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
