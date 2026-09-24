package config

import (
	"slices"
	"strings"
	"testing"
)

func TestProductionURLDefaults(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "")
	t.Setenv("ASSETS_CDN_BASE", "")
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("SMTP_FROM_EMAIL", "")
	t.Setenv("WEBSITE_URL", "")
	t.Setenv("BRAND_LOGO_URL", "")

	cfg := Load()
	origins := strings.Split(cfg.AllowedOrigins, ",")
	for _, origin := range []string{"https://atlxpres.com", "https://admin.atlxpres.com"} {
		if !slices.Contains(origins, origin) {
			t.Fatalf("default CORS origins %q do not include %q", cfg.AllowedOrigins, origin)
		}
	}
	if cfg.PublicBaseURL != "https://api.atlxpres.com" {
		t.Fatalf("PublicBaseURL = %q", cfg.PublicBaseURL)
	}
	if cfg.AssetsCDNBase != "https://media.atlxpres.com" {
		t.Fatalf("AssetsCDNBase = %q", cfg.AssetsCDNBase)
	}
	if cfg.WebsiteURL != "https://atlxpres.com" {
		t.Fatalf("WebsiteURL = %q", cfg.WebsiteURL)
	}
	if cfg.SMTPFromEmail != "welcome@atlxpres.com" {
		t.Fatalf("SMTPFromEmail = %q", cfg.SMTPFromEmail)
	}
}
