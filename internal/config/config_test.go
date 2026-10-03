package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestProviderSubscriptionLaunchWindow(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	before := start.Add(-time.Second)
	if (Config{}).ProviderSubscriptionsRequired(start) {
		t.Fatal("subscriptions should be free by default")
	}
	cfg := Config{ProviderSubscriptionsEnforced: true, ProviderSubscriptionsStartAt: &start}
	if cfg.ProviderSubscriptionsRequired(before) || !cfg.ProviderSubscriptionsRequired(start) {
		t.Fatal("subscription enforcement did not switch at the configured instant")
	}
	cfg.ProviderSubscriptionsStartAt = nil
	if !cfg.ProviderSubscriptionsRequired(before) {
		t.Fatal("enforcement without a start date should be immediate")
	}
}

func TestInvalidSubscriptionStartFailsConfiguration(t *testing.T) {
	t.Setenv("PROVIDER_SUBSCRIPTIONS_START_AT", "next week")
	defer func() {
		if recover() == nil {
			t.Fatal("invalid subscription start must not activate paid checkout immediately")
		}
	}()
	_ = Load()
}

func TestProductionURLDefaults(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "")
	t.Setenv("ASSETS_CDN_BASE", "")
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("SMTP_FROM_EMAIL", "")
	t.Setenv("DEFAULT_FROM_EMAIL", "")
	t.Setenv("WEBSITE_URL", "")
	t.Setenv("PROVIDER_PORTAL_URL", "")
	t.Setenv("BRAND_LOGO_URL", "")

	cfg := Load()
	if cfg.DatabaseMaxConns != 20 || cfg.DatabaseMinConns != 2 {
		t.Fatalf("unexpected database pool defaults: max=%d min=%d", cfg.DatabaseMaxConns, cfg.DatabaseMinConns)
	}
	if cfg.RateLimitPerMinute != 600 || cfg.PublicCacheTTLSeconds != 5 {
		t.Fatalf("unexpected scale defaults: rate=%d cache=%d", cfg.RateLimitPerMinute, cfg.PublicCacheTTLSeconds)
	}
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
	if cfg.ProviderPortalURL != "https://provider.atlxpres.com" {
		t.Fatalf("ProviderPortalURL = %q", cfg.ProviderPortalURL)
	}
	if cfg.SMTPFromEmail != "welcome@atlxpres.com" {
		t.Fatalf("SMTPFromEmail = %q", cfg.SMTPFromEmail)
	}
	if cfg.SMTPFromName != "Atlantic Express" {
		t.Fatalf("SMTPFromName = %q", cfg.SMTPFromName)
	}
	if cfg.SMTPReplyTo != "support@atlxpres.com" {
		t.Fatalf("SMTPReplyTo = %q", cfg.SMTPReplyTo)
	}
}

func TestLegacyEmailDomainCannotOverrideMigratedSender(t *testing.T) {
	t.Setenv("SMTP_FROM_EMAIL", "welcome@sportbanter.online")
	t.Setenv("SMTP_REPLY_TO", "support@sportbanter.online")

	cfg := Load()
	if cfg.SMTPFromEmail != "welcome@atlxpres.com" {
		t.Fatalf("SMTPFromEmail = %q", cfg.SMTPFromEmail)
	}
	if cfg.SMTPReplyTo != "support@atlxpres.com" {
		t.Fatalf("SMTPReplyTo = %q", cfg.SMTPReplyTo)
	}
}

func TestSMTPEnvironmentAliases(t *testing.T) {
	t.Setenv("SMTP_USERNAME", "")
	t.Setenv("SMTP_PASSWORD", "")
	t.Setenv("SMTP_FROM_EMAIL", "")
	t.Setenv("SMTP_FROM_NAME", "")
	t.Setenv("SMTP_USER", "alias-user")
	t.Setenv("SMTP_PASS", "alias-password")
	t.Setenv("DEFAULT_FROM_EMAIL", "support@atlxpres.com")
	t.Setenv("DEFAULT_FROM_NAME", "Atlantic Express Support")

	cfg := Load()
	if cfg.SMTPUsername != "alias-user" {
		t.Fatalf("SMTPUsername = %q", cfg.SMTPUsername)
	}
	if cfg.SMTPPassword != "alias-password" {
		t.Fatalf("SMTPPassword = %q", cfg.SMTPPassword)
	}
	if cfg.SMTPFromEmail != "support@atlxpres.com" {
		t.Fatalf("SMTPFromEmail = %q", cfg.SMTPFromEmail)
	}
	if cfg.SMTPFromName != "Atlantic Express Support" {
		t.Fatalf("SMTPFromName = %q", cfg.SMTPFromName)
	}
}
